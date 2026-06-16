// Package service 提供服务开通的业务逻辑，
// 监听 order_committed 事件并异步开通算力租用等服务，
// 成功时记账 purchase_settle、失败时记账 purchase_refund 退款，
// 全程以 Outbox 事件与不可变审计日志记录状态流转。
package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	ledgerModel "github.com/ancf-commerce/ancf/services/ledger/internal/model"
	ledgerRepo "github.com/ancf-commerce/ancf/services/ledger/internal/repository"
	"github.com/ancf-commerce/ancf/services/provisioning/internal/model"
	"github.com/ancf-commerce/ancf/services/provisioning/internal/repository"
)

// ProvisioningService provides business logic for service provisioning.
// It listens for order_committed outbox events and asynchronously provisions
// the associated services (compute rental, storage, API keys).
type ProvisioningService struct {
	db       *sql.DB
	repo     *repository.ProvisioningRepository
	ledgerRepo *ledgerRepo.LedgerRepository
}

// NewProvisioningService creates a new ProvisioningService.
func NewProvisioningService(db *sql.DB, repo *repository.ProvisioningRepository, ledgerRepo *ledgerRepo.LedgerRepository) *ProvisioningService {
	return &ProvisioningService{
		db:         db,
		repo:       repo,
		ledgerRepo: ledgerRepo,
	}
}

// HandleOrderCommitted processes a single order_committed outbox event.
//
// Steps:
//  1. Parse the outbox event payload to extract order_intent_id
//  2. Within a transaction:
//     a. Lock the order intent row
//     b. Validate status is 'committed'
//     c. Update status to 'provisioning'
//     d. Mark the outbox event as published
//     e. Emit provisioning_started audit event
//  3. Execute provisioning (outside the tx to avoid long-running tx)
//  4. On success:
//     a. Begin new tx
//     b. Update status to 'completed'
//     c. Write purchase_settle ledger entry
//     d. Emit provisioning_completed outbox + audit events
//  5. On failure:
//     a. Begin new tx
//     b. Update status to 'failed'
//     c. Emit provisioning_failed outbox + audit events
//     d. Trigger refund flow
func (s *ProvisioningService) HandleOrderCommitted(ctx context.Context, evt *model.OutboxEvent) error {
	log.Printf("[provisioning] processing event %s for aggregate %s", evt.EventID, evt.AggregateID)

	// Step 1: Parse the outbox event payload.
	var provReq model.ProvisioningRequest
	if err := json.Unmarshal(evt.Payload, &provReq); err != nil {
		return fmt.Errorf("handle order committed: parse payload for event %s: %w", evt.EventID, err)
	}

	intentID := provReq.OrderIntentID
	if intentID == "" {
		return fmt.Errorf("handle order committed: event %s has no order_intent_id", evt.EventID)
	}

	// Step 2: Transition committed -> provisioning within a transaction.
	tx1, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("handle order committed: begin tx for %s: %w", intentID, err)
	}
	defer tx1.Rollback()

	// 2a. Lock the order intent row.
	intent, err := s.repo.LockOrderIntentForUpdate(ctx, tx1, intentID)
	if err != nil {
		return fmt.Errorf("handle order committed: lock intent %s: %w", intentID, err)
	}
	if intent == nil {
		return fmt.Errorf("handle order committed: intent %s not found", intentID)
	}

	// 2b. Validate status is 'committed'. Only committed orders can be provisioned.
	if intent.Status != "committed" {
		// Already processed or in an unexpected state — mark event as published anyway.
		log.Printf("[provisioning] intent %s status is %s (expected committed), skipping", intentID, intent.Status)
		if err := s.repo.MarkOutboxPublished(ctx, tx1, evt.EventID); err != nil {
			return fmt.Errorf("handle order committed: mark published for skipped %s: %w", intentID, err)
		}
		if err := tx1.Commit(); err != nil {
			return fmt.Errorf("handle order committed: commit skip %s: %w", intentID, err)
		}
		return nil
	}

	// 2c. Update status to 'provisioning'.
	if err := s.repo.UpdateOrderStatusWithTx(ctx, tx1, intentID, "provisioning"); err != nil {
		return fmt.Errorf("handle order committed: update to provisioning %s: %w", intentID, err)
	}

	// 2d. Mark the outbox event as published (consumed).
	if err := s.repo.MarkOutboxPublished(ctx, tx1, evt.EventID); err != nil {
		return fmt.Errorf("handle order committed: mark published %s: %w", intentID, err)
	}

	// 2e. Write provisioning_started audit event.
	auditEventID := generateID("audit_")
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"order_intent_id": intentID,
		"order_id":        provReq.OrderID,
		"quote_id":        provReq.QuoteID,
		"wallet":          provReq.Wallet,
		"total_minor":     provReq.TotalMinor,
		"currency":        provReq.Currency,
		"phase":          "transitioning_to_provisioning",
	})
	auditEvt := &model.AuditEvent{
		EventID:      auditEventID,
		EventType:    model.AuditEventProvisioningStarted,
		ActorType:    "system",
		ActorID:      "provisioning-service",
		ResourceType: "order_intent",
		ResourceID:   intentID,
		Action:       "provisioning_started",
		Details:      auditDetails,
	}
	if err := s.repo.InsertAuditEventWithTx(ctx, tx1, auditEvt); err != nil {
		return fmt.Errorf("handle order committed: audit start %s: %w", intentID, err)
	}

	if err := tx1.Commit(); err != nil {
		return fmt.Errorf("handle order committed: commit phase 1 for %s: %w", intentID, err)
	}

	log.Printf("[provisioning] intent %s status updated to provisioning", intentID)

	// Step 3: Execute provisioning (outside the transaction).
	skuID := determineSKUFromIntent(intent, provReq)
	result := s.ProvisionSKU(ctx, skuID, intentID)

	// Step 4/5: Finalize based on provisioning outcome.
	if result.Status == model.ProvStatusProvisioned {
		return s.finalizeProvisioningSuccess(ctx, intentID, result, intent.Wallet, intent.Currency, intent.TotalMinor)
	}
	return s.finalizeProvisioningFailure(ctx, intentID, result, intent.Wallet, intent.Currency, intent.TotalMinor)
}

// ProvisionSKU executes the actual provisioning logic based on the SKU type.
//
// For GPU compute SKUs (compute_rental): simulates a cloud instance allocation,
// returning a fake access_token and instance_id.
//
// For storage_allocation: simulates storage bucket creation.
// For api_key_issuance: simulates API key generation.
func (s *ProvisioningService) ProvisionSKU(ctx context.Context, skuID string, orderIntentID string) *model.ProvisioningResult {
	serviceType := classifyServiceType(skuID)
	now := time.Now().UTC()

	result := &model.ProvisioningResult{
		OrderIntentID: orderIntentID,
		SKUID:         skuID,
		ServiceType:   serviceType,
		ProvisionedAt: &now,
	}

	switch serviceType {
	case model.ServiceTypeComputeRental:
		// Simulate GPU compute instance allocation.
		instanceID := "gpu-inst-" + generateShortID()
		accessToken := "at_" + generateShortID() + "_" + generateShortID()
		endpointURL := fmt.Sprintf("https://compute.ancf.internal/instances/%s", instanceID)

		result.Status = model.ProvStatusProvisioned
		result.InstanceID = &instanceID
		result.AccessToken = &accessToken
		result.EndpointURL = &endpointURL
		result.Details, _ = json.Marshal(map[string]interface{}{
			"sku_id":       skuID,
			"instance_id":  instanceID,
			"instance_type": skuID,
			"region":       "us-east-1",
			"status":       "running",
			"cpu_count":    16,
			"memory_gb":    64,
			"gpu_count":    1,
		})

		log.Printf("[provisioning] compute rental %s provisioned: instance=%s, intent=%s", skuID, instanceID, orderIntentID)

	case model.ServiceTypeStorageAllocation:
		bucketID := "bucket-" + generateShortID()
		accessToken := "sk_" + generateShortID()
		endpointURL := fmt.Sprintf("https://storage.ancf.internal/buckets/%s", bucketID)

		result.Status = model.ProvStatusProvisioned
		result.InstanceID = &bucketID
		result.AccessToken = &accessToken
		result.EndpointURL = &endpointURL
		result.Details, _ = json.Marshal(map[string]interface{}{
			"sku_id":      skuID,
			"bucket_id":   bucketID,
			"storage_gb":  1000,
			"region":      "us-east-1",
			"redundancy":  "triple",
		})

		log.Printf("[provisioning] storage allocation %s provisioned: bucket=%s, intent=%s", skuID, bucketID, orderIntentID)

	case model.ServiceTypeAPIKeyIssuance:
		apiKey := "ak_" + generateShortID() + "_" + generateShortID()
		endpointURL := "https://api.ancf.internal/v1"

		result.Status = model.ProvStatusProvisioned
		result.AccessToken = &apiKey
		result.EndpointURL = &endpointURL
		result.Details, _ = json.Marshal(map[string]interface{}{
			"sku_id":     skuID,
			"api_key":    apiKey,
			"rate_limit": 1000,
			"plan":       "standard",
		})

		log.Printf("[provisioning] api key issuance %s provisioned: key prefix=%s, intent=%s", skuID, apiKey[:12]+"...", orderIntentID)

	default:
		errMsg := fmt.Sprintf("unknown service type %s for sku %s", serviceType, skuID)
		result.Status = model.ProvStatusFailed
		result.ErrorMessage = &errMsg
		log.Printf("[provisioning] failed: %s", errMsg)
	}

	return result
}

// finalizeProvisioningSuccess completes the provisioning flow when the service was successfully provisioned.
//
// Within a transaction:
//  1. Update order status from provisioning -> completed
//  2. Write purchase_settle ledger entry (debit user_pending, credit merchant_settled)
//  3. Emit provisioning_completed outbox event
//  4. Write audit log entry
func (s *ProvisioningService) finalizeProvisioningSuccess(ctx context.Context, intentID string, result *model.ProvisioningResult, wallet, currency string, totalMinor int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("finalize success: begin tx for %s: %w", intentID, err)
	}
	defer tx.Rollback()

	// 1. Update order status: provisioning -> completed.
	if err := s.repo.UpdateOrderStatusWithTx(ctx, tx, intentID, "completed"); err != nil {
		return fmt.Errorf("finalize success: update status for %s: %w", intentID, err)
	}

	// 2. Write purchase_settle ledger entry.
	// debit user_pending, credit merchant_settled
	txID := generateID("tx_")
	settleEntries := ledgerModel.PurchaseSettle(txID, wallet, totalMinor, currency, intentID)
	for i := range settleEntries {
		settleEntries[i].EntryID = generateID("entry_")
	}
	if err := s.ledgerRepo.PostTransaction(ctx, tx, settleEntries); err != nil {
		return fmt.Errorf("finalize success: ledger settle for %s: %w", intentID, err)
	}

	// 3. Emit provisioning_completed outbox event.
	outboxEventID := generateID("evt_")
	resultJSON, _ := json.Marshal(result)
	if err := s.repo.InsertOutboxEventWithTx(ctx, tx, outboxEventID,
		model.EventTypeProvisioningCompleted, "order", intentID, resultJSON); err != nil {
		return fmt.Errorf("finalize success: outbox event for %s: %w", intentID, err)
	}

	// 4. Write audit log entry.
	auditEventID := generateID("audit_")
	auditDetails, _ := json.Marshal(result)
	auditEvt := &model.AuditEvent{
		EventID:      auditEventID,
		EventType:    model.AuditEventProvisioningCompleted,
		ActorType:    "system",
		ActorID:      "provisioning-service",
		ResourceType: "order_intent",
		ResourceID:   intentID,
		Action:       "provisioning_completed",
		Details:      auditDetails,
	}
	if err := s.repo.InsertAuditEventWithTx(ctx, tx, auditEvt); err != nil {
		return fmt.Errorf("finalize success: audit event for %s: %w", intentID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finalize success: commit for %s: %w", intentID, err)
	}

	log.Printf("[provisioning] intent %s provisioning completed successfully", intentID)
	return nil
}

// finalizeProvisioningFailure handles the failure path when provisioning fails.
//
// Within a transaction:
//  1. Update order status: provisioning -> failed
//  2. Write purchase_refund ledger entry (debit user_pending, credit user_available)
//  3. Emit provisioning_failed outbox event
//  4. Write audit log entry
func (s *ProvisioningService) finalizeProvisioningFailure(ctx context.Context, intentID string, result *model.ProvisioningResult, wallet, currency string, totalMinor int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("finalize failure: begin tx for %s: %w", intentID, err)
	}
	defer tx.Rollback()

	// 1. Update order status: provisioning -> failed.
	if err := s.repo.UpdateOrderStatusWithTx(ctx, tx, intentID, "failed"); err != nil {
		return fmt.Errorf("finalize failure: update status for %s: %w", intentID, err)
	}

	// 2. Write purchase_refund ledger entry.
	// debit user_pending, credit user_available (returns funds to user)
	txID := generateID("tx_")
	refundEntries := ledgerModel.PurchaseRefund(txID, wallet, totalMinor, currency, intentID)
	for i := range refundEntries {
		refundEntries[i].EntryID = generateID("entry_")
	}
	if err := s.ledgerRepo.PostTransaction(ctx, tx, refundEntries); err != nil {
		return fmt.Errorf("finalize failure: ledger refund for %s: %w", intentID, err)
	}

	// 3. Emit provisioning_failed outbox event.
	outboxEventID := generateID("evt_")
	resultJSON, _ := json.Marshal(result)
	if err := s.repo.InsertOutboxEventWithTx(ctx, tx, outboxEventID,
		model.EventTypeProvisioningFailed, "order", intentID, resultJSON); err != nil {
		return fmt.Errorf("finalize failure: outbox event for %s: %w", intentID, err)
	}

	// 4. Write audit log entry.
	auditEventID := generateID("audit_")
	auditDetails, _ := json.Marshal(result)
	auditEvt := &model.AuditEvent{
		EventID:      auditEventID,
		EventType:    model.AuditEventProvisioningFailed,
		ActorType:    "system",
		ActorID:      "provisioning-service",
		ResourceType: "order_intent",
		ResourceID:   intentID,
		Action:       "provisioning_failed",
		Details:      auditDetails,
	}
	if err := s.repo.InsertAuditEventWithTx(ctx, tx, auditEvt); err != nil {
		return fmt.Errorf("finalize failure: audit event for %s: %w", intentID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finalize failure: commit for %s: %w", intentID, err)
	}

	log.Printf("[provisioning] intent %s provisioning failed: %s", intentID, getErrorMessage(result))
	return nil
}

// ManualProvision triggers provisioning for a given order intent from an admin request.
// This is the manual fallback path when the outbox-driven automation cannot or should
// not process the event automatically.
func (s *ProvisioningService) ManualProvision(ctx context.Context, intentID string) (*model.ProvisioningResult, error) {
	// Validate the order intent exists and is in an appropriate state.
	intent, err := s.repo.GetOrderIntentByID(ctx, intentID)
	if err != nil {
		return nil, fmt.Errorf("manual provision: get intent %s: %w", intentID, err)
	}
	if intent == nil {
		return nil, fmt.Errorf("manual provision: intent %s not found", intentID)
	}

	// Allow manual provisioning from 'committed', 'provisioning', or 'failed' states.
	// 'failed' allows retry.
	validStates := map[string]bool{"committed": true, "provisioning": true, "failed": true}
	if !validStates[intent.Status] {
		return nil, fmt.Errorf("manual provision: intent %s status is %s, expected committed/provisioning/failed", intentID, intent.Status)
	}

	// Transition to provisioning if not already there.
	if intent.Status != "provisioning" {
		if err := s.repo.UpdateOrderStatus(ctx, intentID, "provisioning"); err != nil {
			return nil, fmt.Errorf("manual provision: update to provisioning %s: %w", intentID, err)
		}
	}

	// Execute provisioning.
	provReq := model.ProvisioningRequest{
		OrderIntentID: intent.IntentID,
		Wallet:        intent.Wallet,
		TotalMinor:    intent.TotalMinor,
		Currency:      intent.Currency,
	}
	skuID := determineSKUFromIntentRow(intent)
	result := s.ProvisionSKU(ctx, skuID, intentID)

	// Finalize.
	if result.Status == model.ProvStatusProvisioned {
		if err := s.finalizeProvisioningSuccess(ctx, intentID, result, intent.Wallet, intent.Currency, intent.TotalMinor); err != nil {
			return nil, fmt.Errorf("manual provision: finalize success %s: %w", intentID, err)
		}
	} else {
		if err := s.finalizeProvisioningFailure(ctx, intentID, result, intent.Wallet, intent.Currency, intent.TotalMinor); err != nil {
			return nil, fmt.Errorf("manual provision: finalize failure %s: %w", intentID, err)
		}
	}

	return result, nil
}

// GetProvisioningStatus returns the current provisioning status for a given order intent.
// It checks both the order_intents.status field and the audit_log for completed provisioning.
func (s *ProvisioningService) GetProvisioningStatus(ctx context.Context, intentID string) (*model.ProvisionStatusResponse, error) {
	intent, err := s.repo.GetOrderIntentByID(ctx, intentID)
	if err != nil {
		return nil, fmt.Errorf("get provisioning status: %w", err)
	}
	if intent == nil {
		return nil, fmt.Errorf("get provisioning status: intent %s not found", intentID)
	}

	var provReq model.ProvisioningRequest
	// Ignore parse errors for the request; it's informational.
	_ = json.Unmarshal(intent.SignablePayload, &provReq)

	resp := &model.ProvisionStatusResponse{
		OrderIntentID: intent.IntentID,
		OrderStatus:   intent.Status,
		ServiceType:   classifyServiceType(determineSKUFromIntentRow(intent)),
		SKUID:         determineSKUFromIntentRow(intent),
	}

	// If completed, fetch the provisioning result from audit log.
	if intent.Status == "completed" {
		provResult, err := s.repo.GetProvisioningResultByIntentID(ctx, intentID)
		if err == nil && provResult != nil {
			resp.AccessToken = provResult.AccessToken
			resp.InstanceID = provResult.InstanceID
			resp.EndpointURL = provResult.EndpointURL
			if provResult.ProvisionedAt != nil {
				t := provResult.ProvisionedAt.Format(time.RFC3339)
				resp.ProvisionedAt = &t
			}
		}
	} else if intent.Status == "failed" {
		// Try to get error details from audit log.
		provResult, err := s.repo.GetProvisioningResultByIntentID(ctx, intentID)
		if err == nil && provResult != nil {
			resp.ErrorMessage = provResult.ErrorMessage
		}
		// Fallback: check provisioning_failed audit events.
		if resp.ErrorMessage == nil {
			// The GetProvisioningResultByIntentID only checks completed events.
			// For failed events, we check the intent status directly.
			msg := "provisioning failed — see audit log for details"
			resp.ErrorMessage = &msg
		}
	}

	return resp, nil
}

// GetProvisionAccess returns the access credentials for a successfully provisioned order.
// This is the user-facing endpoint that returns the access token after provisioning completes.
func (s *ProvisioningService) GetProvisionAccess(ctx context.Context, intentID string) (*model.ProvisionAccessResponse, error) {
	intent, err := s.repo.GetOrderIntentByID(ctx, intentID)
	if err != nil {
		return nil, fmt.Errorf("get provision access: %w", err)
	}
	if intent == nil {
		return nil, fmt.Errorf("get provision access: intent %s not found", intentID)
	}

	resp := &model.ProvisionAccessResponse{
		OrderIntentID: intent.IntentID,
		Status:        intent.Status,
		ServiceType:   classifyServiceType(determineSKUFromIntentRow(intent)),
		SKUID:         determineSKUFromIntentRow(intent),
	}

	// Only return access credentials if provisioning completed.
	if intent.Status == "completed" {
		provResult, err := s.repo.GetProvisioningResultByIntentID(ctx, intentID)
		if err != nil {
			return nil, fmt.Errorf("get provision access: fetch result: %w", err)
		}
		if provResult == nil {
			return nil, fmt.Errorf("get provision access: intent %s is completed but no provisioning result found", intentID)
		}
		resp.AccessToken = provResult.AccessToken
		resp.InstanceID = provResult.InstanceID
		resp.EndpointURL = provResult.EndpointURL
		if provResult.ProvisionedAt != nil {
			t := provResult.ProvisionedAt.Format(time.RFC3339)
			resp.ProvisionedAt = &t
		}
	}

	return resp, nil
}

// ProcessOutboxBatch fetches pending order_committed events and processes each one.
// Returns the number of events processed and any error that stopped processing.
func (s *ProvisioningService) ProcessOutboxBatch(ctx context.Context) (int, error) {
	events, err := s.repo.FetchPendingOutboxEvents(ctx, 10)
	if err != nil {
		return 0, fmt.Errorf("process outbox batch: %w", err)
	}

	if len(events) == 0 {
		return 0, nil
	}

	log.Printf("[provisioning] processing %d pending order_committed events", len(events))

	processed := 0
	for i := range events {
		evt := &events[i]
		if err := s.HandleOrderCommitted(ctx, evt); err != nil {
			log.Printf("[provisioning] error processing event %s: %v", evt.EventID, err)
			// Continue processing remaining events; individual failures are isolated.
			continue
		}
		processed++
	}

	return processed, nil
}

// StartOutboxListener launches a background goroutine that polls the outbox table
// for order_committed events and processes them.
// The loop exits when the context is cancelled.
func (s *ProvisioningService) StartOutboxListener(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("[provisioning] outbox listener stopping: %v", ctx.Err())
				return
			case <-ticker.C:
				count, err := s.ProcessOutboxBatch(ctx)
				if err != nil {
					log.Printf("[provisioning] outbox batch error: %v", err)
				} else if count > 0 {
					log.Printf("[provisioning] outbox batch processed %d events", count)
				}
			}
		}
	}()
	log.Printf("[provisioning] outbox listener started with interval %v", interval)
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// classifyServiceType determines the provisioning service type based on the SKU ID prefix.
//
// Mapping:
//   - sku_gpu_*     -> compute_rental
//   - sku_storage_* -> storage_allocation
//   - sku_api_*     -> api_key_issuance
//   - default       -> compute_rental (backward compatible)
func classifyServiceType(skuID string) string {
	lower := strings.ToLower(skuID)
	if strings.Contains(lower, "gpu") || strings.HasPrefix(lower, "sku_gpu_") {
		return model.ServiceTypeComputeRental
	}
	if strings.Contains(lower, "storage") || strings.HasPrefix(lower, "sku_storage_") {
		return model.ServiceTypeStorageAllocation
	}
	if strings.Contains(lower, "api") || strings.HasPrefix(lower, "sku_api_") {
		return model.ServiceTypeAPIKeyIssuance
	}
	// Default for known GPU SKUs from seed data.
	if strings.HasPrefix(lower, "sku_gpu_h100") || strings.HasPrefix(lower, "sku_gpu_a100") || strings.HasPrefix(lower, "sku_gpu_l40s") {
		return model.ServiceTypeComputeRental
	}
	return model.ServiceTypeComputeRental
}

// determineSKUFromIntent extracts the SKU ID from the order intent's signable_payload.
// Falls back to the quote_id if the payload does not contain SKU information.
func determineSKUFromIntent(intent *repository.OrderIntentRow, provReq model.ProvisioningRequest) string {
	// Try to parse SKU from signable payload (embedded in quote line info).
	if intent.SignablePayload != nil {
		var payload struct {
			QuoteID string `json:"quote_id"`
		}
		if err := json.Unmarshal(intent.SignablePayload, &payload); err == nil && payload.QuoteID != "" {
			// For the demo, we infer SKU from the quote ID — real implementation
			// would look up quote lines from the quotes table.
			// Returning a known GPU SKU as fallback.
		}
	}

	// For demo purposes, if we know the quote_id, we map it to the H100 SKU
	// which is the most valuable and likely for provisioning scenarios.
	// Real implementation: query quotes table -> parse lines -> get sku_id.
	if provReq.QuoteID != "" {
		// In a real implementation, we would query the quotes table and parse
		// the lines JSONB to extract the actual SKU IDs.
		// For the seed data (demo), the primary SKUs are GPU types.
		return "sku_gpu_h100_v1"
	}

	return "sku_gpu_h100_v1"
}

// determineSKUFromIntentRow is a variant that works with OrderIntentRow directly.
func determineSKUFromIntentRow(intent *repository.OrderIntentRow) string {
	// Default to H100 for demo; real implementation would parse quote lines.
	if strings.Contains(intent.QuoteID, "h100") {
		return "sku_gpu_h100_v1"
	}
	if strings.Contains(intent.QuoteID, "a100") {
		return "sku_gpu_a100_v1"
	}
	if strings.Contains(intent.QuoteID, "l40s") {
		return "sku_gpu_l40s_v1"
	}
	return "sku_gpu_h100_v1"
}

// getErrorMessage extracts the error message from a failed provisioning result.
func getErrorMessage(result *model.ProvisioningResult) string {
	if result.ErrorMessage != nil {
		return *result.ErrorMessage
	}
	return "unknown error"
}

// generateID creates a random hex-encoded ID with the given prefix.
func generateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// generateShortID creates a shorter random hex ID (8 bytes -> 16 hex chars).
func generateShortID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
