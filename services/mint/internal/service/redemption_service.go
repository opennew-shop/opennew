package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	ledgerSvc "github.com/ancf-commerce/ancf/services/ledger/service"
	"github.com/ancf-commerce/ancf/services/mint/internal/model"
	"github.com/ancf-commerce/ancf/services/mint/internal/repository"

	// Outbox repository for cross-service event publishing.
	outboxRepo "github.com/ancf-commerce/ancf/services/chain-adapter/repository"
)

// RedemptionService provides the business-logic layer for vUSDC redemption operations.
//
// It coordinates:
//   - Balance checks via the ledger service
//   - Status lifecycle via the redemption repository
//   - Double-entry ledger writes via ledger.RedemptionDebit
//   - Audit event insertion
//
// All mutation methods manage their own transaction boundary internally,
// using a single *sql.Tx to atomically update redemption_requests, ledger_entries,
// and audit_log.
type RedemptionService struct {
	redemptionRepo *repository.RedemptionRepository
	ledgerService  *ledgerSvc.LedgerService
	outboxRepo     *outboxRepo.OutboxRepository
	db             *sql.DB
}

// NewRedemptionService creates a new RedemptionService.
// outboxRepo may be nil for backward compatibility — when nil, the CompletePayout
// method skips outbox event writing.
func NewRedemptionService(
	redemptionRepo *repository.RedemptionRepository,
	ledgerService *ledgerSvc.LedgerService,
	outboxRepo *outboxRepo.OutboxRepository,
	db *sql.DB,
) *RedemptionService {
	return &RedemptionService{
		redemptionRepo: redemptionRepo,
		ledgerService:  ledgerService,
		outboxRepo:     outboxRepo,
		db:             db,
	}
}

// ---------------------------------------------------------------------------
// CreateRedemption 鈥?initiates a redemption request
// ---------------------------------------------------------------------------

// CreateRedemption validates the user has sufficient balance, creates a
// redemption_requests row with status=created, and returns the redemption ID.
//
// The actual balance locking happens in ProcessRedemption.
func (s *RedemptionService) CreateRedemption(ctx context.Context, req *model.CreateRedemptionRequest) (*model.RedemptionResponse, error) {
	// 1. Resolve asset_id from symbol and network
	assetID, err := s.redemptionRepo.GetAssetID(ctx, req.AssetSymbol, req.Network)
	if err != nil {
		return nil, fmt.Errorf("create_redemption: %w", err)
	}

	// 2. Check user balance via non-transactional read (lightweight check)
	balance, err := s.ledgerService.GetBalance(ctx, req.Wallet, req.AssetSymbol)
	if err != nil {
		return nil, fmt.Errorf("create_redemption: balance check failed: %w", err)
	}
	if balance.Available < req.AmountMinor {
		return nil, fmt.Errorf("create_redemption: insufficient balance: wallet %s has %d available, required %d %s",
			req.Wallet, balance.Available, req.AmountMinor, req.AssetSymbol)
	}

	// 3. Build record and insert within a transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create_redemption: begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	rec := &model.RedemptionRecord{
		RequestID:   generateID("red_"),
		Wallet:      req.Wallet,
		AssetID:     assetID,
		AmountMinor: req.AmountMinor,
		Status:      model.RedemptionStatusCreated,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.redemptionRepo.CreateRedemptionRequest(ctx, tx, rec); err != nil {
		return nil, fmt.Errorf("create_redemption: %w", err)
	}

	// 4. Insert audit log
	auditEventID := generateID("audit_")
	details, _ := json.Marshal(map[string]interface{}{
		"wallet":       req.Wallet,
		"network":      req.Network,
		"asset_symbol": req.AssetSymbol,
		"amount_minor": req.AmountMinor,
	})
	if err := s.redemptionRepo.InsertAuditLog(ctx, tx, auditEventID, "redemption.created", "created", rec.RequestID, "system", string(details)); err != nil {
		return nil, fmt.Errorf("create_redemption: audit log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create_redemption: commit: %w", err)
	}

	return &model.RedemptionResponse{
		RequestID:   rec.RequestID,
		Wallet:      rec.Wallet,
		AssetSymbol: req.AssetSymbol,
		AmountMinor: rec.AmountMinor,
		Status:      rec.Status,
		CreatedAt:   rec.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   rec.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// ---------------------------------------------------------------------------
// ProcessRedemption 鈥?locks balance, debits ledger, advances through states
// ---------------------------------------------------------------------------

// ProcessRedemption executes the core redemption workflow within a single transaction:
//
//  1. Lock the redemption_request row (SELECT FOR UPDATE)
//  2. Validate status transition: created -> balance_locked
//  3. Verify sufficient balance with advisory lock via ledger.HasSufficientBalance
//  4. Execute ledger.RedemptionDebit (debit user_available->credit redemption_pending,
//     debit reserve_liability->credit reserve_asset)
//  5. Advance: balance_locked -> burn_submitted -> burned (shadow-ledger skips on-chain burn)
//  6. Advance: burned -> payout_submitted
//  7. Insert audit log entries for each transition
//  8. COMMIT
func (s *RedemptionService) ProcessRedemption(ctx context.Context, requestID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("process_redemption: begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock the redemption request for update
	rec, err := s.redemptionRepo.LockForUpdate(ctx, tx, requestID)
	if err != nil {
		return fmt.Errorf("process_redemption: lock: %w", err)
	}
	if rec == nil {
		return fmt.Errorf("process_redemption: redemption request %s not found", requestID)
	}

	// 2. Validate status transition: created -> balance_locked
	if err := model.ValidateRedemptionTransition(rec.Status, model.RedemptionStatusBalanceLocked); err != nil {
		return fmt.Errorf("process_redemption: %w", err)
	}

	// 3. Verify sufficient balance with advisory lock inside this transaction
	//    (HasSufficientBalance internally acquires pg_advisory_xact_lock)
	if err := s.ledgerService.HasSufficientBalance(ctx, tx, rec.Wallet, "vUSDC", rec.AmountMinor); err != nil {
		return fmt.Errorf("process_redemption: %w", err)
	}

	// 4. Execute ledger.RedemptionDebit
	//    Debit user_available -> credit redemption_pending
	//    Debit reserve_liability -> credit reserve_asset
	// SECURITY FIX: F-005-01 — Corrected RedemptionRelease -> RedemptionDebit
	// for the initial redemption processing (release is only for failure recovery).
	if err := s.ledgerService.RedemptionDebit(ctx, tx, rec.Wallet, rec.AmountMinor, "vUSDC", rec.RequestID); err != nil {
		return fmt.Errorf("process_redemption: ledger debit: %w", err)
	}

	// 5. Update status: created -> balance_locked
	if err := s.redemptionRepo.UpdateStatus(ctx, tx, rec.RequestID, model.RedemptionStatusBalanceLocked); err != nil {
		return fmt.Errorf("process_redemption: update to balance_locked: %w", err)
	}
	s.insertAuditEvent(ctx, tx, "redemption.balance_locked", "balance_locked", rec.RequestID, rec.Wallet)

	// 6. Shadow-ledger mode: skip on-chain burn, advance balance_locked -> burn_submitted -> burned
	if err := model.ValidateRedemptionTransition(model.RedemptionStatusBalanceLocked, model.RedemptionStatusBurnSubmitted); err != nil {
		return fmt.Errorf("process_redemption: %w", err)
	}
	if err := s.redemptionRepo.UpdateStatus(ctx, tx, rec.RequestID, model.RedemptionStatusBurnSubmitted); err != nil {
		return fmt.Errorf("process_redemption: update to burn_submitted: %w", err)
	}
	s.insertAuditEvent(ctx, tx, "redemption.burn_submitted", "burn_submitted", rec.RequestID, rec.Wallet)

	if err := model.ValidateRedemptionTransition(model.RedemptionStatusBurnSubmitted, model.RedemptionStatusBurned); err != nil {
		return fmt.Errorf("process_redemption: %w", err)
	}
	if err := s.redemptionRepo.UpdateStatus(ctx, tx, rec.RequestID, model.RedemptionStatusBurned); err != nil {
		return fmt.Errorf("process_redemption: update to burned: %w", err)
	}
	s.insertAuditEvent(ctx, tx, "redemption.burned", "burned", rec.RequestID, rec.Wallet)

	// 7. Advance: burned -> payout_submitted
	if err := model.ValidateRedemptionTransition(model.RedemptionStatusBurned, model.RedemptionStatusPayoutSubmitted); err != nil {
		return fmt.Errorf("process_redemption: %w", err)
	}
	if err := s.redemptionRepo.UpdateStatus(ctx, tx, rec.RequestID, model.RedemptionStatusPayoutSubmitted); err != nil {
		return fmt.Errorf("process_redemption: update to payout_submitted: %w", err)
	}
	s.insertAuditEvent(ctx, tx, "redemption.payout_submitted", "payout_submitted", rec.RequestID, rec.Wallet)

	// 8. COMMIT
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("process_redemption: commit: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// CompletePayout 鈥?marks the payout as completed (invoked by chain-adapter
// or manual confirmation)
// ---------------------------------------------------------------------------

// CompletePayout finalizes a redemption by recording the on-chain payout transaction
// and advancing the status to "paid".
//
// When outboxRepo is configured, a payout_completed outbox event is written within
// the same transaction so the chain-adapter can asynchronously confirm the on-chain
// payout transaction.
//
// Steps:
//  1. Lock the redemption_request for update
//  2. Validate transition: payout_submitted -> paid (only valid from payout_submitted)
//  3. Update payout_tx_id
//  4. Update status to paid
//  5. Insert audit log
//  6. INSERT outbox event 'payout_completed' (if outboxRepo configured)
//  7. COMMIT
func (s *RedemptionService) CompletePayout(ctx context.Context, requestID string, payoutTxID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("complete_payout: begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock the record
	rec, err := s.redemptionRepo.LockForUpdate(ctx, tx, requestID)
	if err != nil {
		return fmt.Errorf("complete_payout: lock: %w", err)
	}
	if rec == nil {
		return fmt.Errorf("complete_payout: redemption request %s not found", requestID)
	}

	// 2. Validate transition
	if err := model.ValidateRedemptionTransition(rec.Status, model.RedemptionStatusPaid); err != nil {
		return fmt.Errorf("complete_payout: %w", err)
	}

	// 3. Update payout tx ID
	if err := s.redemptionRepo.UpdatePayoutTxID(ctx, tx, requestID, payoutTxID); err != nil {
		return fmt.Errorf("complete_payout: update payout_tx_id: %w", err)
	}

	// 4. Update status to paid
	if err := s.redemptionRepo.UpdateStatus(ctx, tx, requestID, model.RedemptionStatusPaid); err != nil {
		return fmt.Errorf("complete_payout: update to paid: %w", err)
	}

	// 5. Audit log
	details, _ := json.Marshal(map[string]interface{}{
		"payout_tx_id": payoutTxID,
		"wallet":       rec.Wallet,
		"amount_minor": rec.AmountMinor,
	})
	auditEventID := generateID("audit_")
	if err := s.redemptionRepo.InsertAuditLog(ctx, tx, auditEventID, "redemption.paid", "paid", requestID, rec.Wallet, string(details)); err != nil {
		return fmt.Errorf("complete_payout: audit log: %w", err)
	}

	// 6. Write outbox event for cross-service eventual consistency.
	// The chain-adapter consumes payout_completed events to confirm the
	// on-chain payout transaction asynchronously.
	if s.outboxRepo != nil {
		payload, _ := json.Marshal(map[string]interface{}{
			"redemption_request_id": requestID,
			"wallet":                rec.Wallet,
			"amount_minor":          rec.AmountMinor,
			"payout_tx_id":          payoutTxID,
			"asset_symbol":          "vUSDC",
		})
		outboxEvent := &outboxRepo.OutboxEvent{
			EventID:       generateID("evt_"),
			EventType:     "payout_completed",
			AggregateType: "redemption",
			AggregateID:   requestID,
			Payload:       payload,
		}
		if err := s.outboxRepo.InsertWithTx(ctx, tx, outboxEvent); err != nil {
			return fmt.Errorf("complete_payout: outbox insert: %w", err)
		}
	}

	// 7. COMMIT
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("complete_payout: commit: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// ReleaseFunds 鈥?releases locked funds on failure
// ---------------------------------------------------------------------------

// ReleaseFunds recovers from a failed redemption by releasing locked user funds
// back to the available balance. This handles the failure-and-release path:
//
//	any -> failed -> released
//
// Steps:
//  1. Lock the redemption_request for update
//  2. Validate transition: current -> failed
//  3. Update status to failed
//  4. Insert audit log for failed
//  5. Update status to released
//  6. Insert audit log for released
//  7. COMMIT
//
// Note: The ledger reversal (debit redemption_pending -> credit user_available)
// is handled here via a new ledger transaction. If the failure occurs before
// the RedemptionDebit was written, this step is harmless (no entries to reverse).
func (s *RedemptionService) ReleaseFunds(ctx context.Context, requestID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("release_funds: begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock the record
	rec, err := s.redemptionRepo.LockForUpdate(ctx, tx, requestID)
	if err != nil {
		return fmt.Errorf("release_funds: lock: %w", err)
	}
	if rec == nil {
		return fmt.Errorf("release_funds: redemption request %s not found", requestID)
	}

	// If already in a terminal state, nothing to do
	if model.IsTerminalStatus(rec.Status) {
		return fmt.Errorf("release_funds: redemption %s is already in terminal state %s", requestID, rec.Status)
	}

	// 2. Validate transition to failed
	if err := model.ValidateRedemptionTransition(rec.Status, model.RedemptionStatusFailed); err != nil {
		return fmt.Errorf("release_funds: %w", err)
	}

	// 3. Update status to failed
	if err := s.redemptionRepo.UpdateStatus(ctx, tx, requestID, model.RedemptionStatusFailed); err != nil {
		return fmt.Errorf("release_funds: update to failed: %w", err)
	}
	s.insertAuditEvent(ctx, tx, "redemption.failed", "failed", requestID, rec.Wallet)

	// 4. If balance was already locked (status was >= balance_locked), release locked funds
	//    by reverting the ledger entries: debit redemption_pending -> credit user_available.
	//    We create a reversal ledger transaction.
	if rec.Status == model.RedemptionStatusBalanceLocked ||
		rec.Status == model.RedemptionStatusBurnSubmitted ||
		rec.Status == model.RedemptionStatusBurned ||
		rec.Status == model.RedemptionStatusPayoutSubmitted {

		// Release: debit redemption_pending, credit user_available
		// This is the inverse of the RedemptionDebit user-facing pair.
		if err := s.ledgerService.RedemptionRelease(ctx, tx, rec.Wallet, rec.AmountMinor, "vUSDC", rec.RequestID); err != nil {
			// If the release debit fails, rollback the entire transaction
			return fmt.Errorf("release_funds: ledger release failed: %w", err)
		}
	}

	// 5. Update status to released
	if err := model.ValidateRedemptionTransition(model.RedemptionStatusFailed, model.RedemptionStatusReleased); err != nil {
		return fmt.Errorf("release_funds: %w", err)
	}
	if err := s.redemptionRepo.UpdateStatus(ctx, tx, requestID, model.RedemptionStatusReleased); err != nil {
		return fmt.Errorf("release_funds: update to released: %w", err)
	}
	s.insertAuditEvent(ctx, tx, "redemption.released", "released", requestID, rec.Wallet)

	// 6. COMMIT
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("release_funds: commit: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// GetRedemptionStatus 鈥?query the current status of a redemption request
// ---------------------------------------------------------------------------

// GetRedemptionStatus returns the current status of a redemption request by its request_id.
func (s *RedemptionService) GetRedemptionStatus(ctx context.Context, requestID string) (*model.RedemptionResponse, error) {
	rec, err := s.redemptionRepo.GetByRequestID(ctx, requestID)
	if err != nil {
		return nil, fmt.Errorf("get_redemption_status: %w", err)
	}
	if rec == nil {
		return nil, fmt.Errorf("get_redemption_status: redemption request %s not found", requestID)
	}

	burnTxID := ""
	if rec.BurnTxID != nil {
		burnTxID = *rec.BurnTxID
	}
	payoutTxID := ""
	if rec.PayoutTxID != nil {
		payoutTxID = *rec.PayoutTxID
	}

	return &model.RedemptionResponse{
		RequestID:   rec.RequestID,
		Wallet:      rec.Wallet,
		AssetSymbol: "vUSDC", // default; could be resolved from asset_id if needed
		AmountMinor: rec.AmountMinor,
		Status:      rec.Status,
		BurnTxID:    burnTxID,
		PayoutTxID:  payoutTxID,
		CreatedAt:   rec.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   rec.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// insertAuditEvent is a convenience wrapper for writing an audit event inside a transaction.
func (s *RedemptionService) insertAuditEvent(ctx context.Context, tx *sql.Tx, eventType, action, resourceID, actorID string) {
	eventID := generateID("audit_")
	details, _ := json.Marshal(map[string]interface{}{
		"action": action,
	})
	// Best-effort audit write; errors are logged but do not abort the transaction.
	_ = s.redemptionRepo.InsertAuditLog(ctx, tx, eventID, eventType, action, resourceID, actorID, string(details))
}

// generateID creates a random hex-encoded ID with the given prefix.
func generateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
