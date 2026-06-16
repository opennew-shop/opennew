// Package service 实现 checkout 结算服务的核心业务逻辑：
// prepare 生成可签名订单意图负载，commit 完成 EdDSA 签名校验、三路幂等解析、
// 8 状态订单状态机校验以及单事务内的报价消费、库存扣减与 Outbox 事件写入；
// 另含签名工具、幂等预检、事务隔离级别决策与 Outbox 事件投递处理器。
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	catalogRepo "github.com/ancf-commerce/ancf/services/catalog/internal/repository"
	"github.com/ancf-commerce/ancf/services/checkout/internal/model"
	"github.com/ancf-commerce/ancf/services/checkout/internal/repository"
	quoteModel "github.com/ancf-commerce/ancf/services/quote/internal/model"
	quoteRepo "github.com/ancf-commerce/ancf/services/quote/internal/repository"
	quoteSvc "github.com/ancf-commerce/ancf/services/quote/internal/service"
)

// CheckoutService provides business logic for the checkout prepare and commit flow.
type CheckoutService struct {
	db           *sql.DB
	orderRepo    *repository.OrderRepository
	quoteRepo    *quoteRepo.QuoteRepository
	quoteService *quoteSvc.QuoteService
	skuRepo      *catalogRepo.SKURepository
	outboxRepo   *repository.OutboxRepository
	domain       string
	shopID       string
}

// NewCheckoutService creates a new CheckoutService.
func NewCheckoutService(db *sql.DB, orderRepo *repository.OrderRepository, quoteRepo *quoteRepo.QuoteRepository, quoteService *quoteSvc.QuoteService, skuRepo *catalogRepo.SKURepository, outboxRepo *repository.OutboxRepository, domain, shopID string) *CheckoutService {
	return &CheckoutService{
		db:           db,
		orderRepo:    orderRepo,
		quoteRepo:    quoteRepo,
		quoteService: quoteService,
		skuRepo:      skuRepo,
		outboxRepo:   outboxRepo,
		domain:       domain,
		shopID:       shopID,
	}
}

// PrepareCheckout generates a canonical signable order intent payload.
//
// Steps:
//  0. SECURITY FIX: F-001-01 — If X-Idempotency-Key provided, check for existing intent (replay)
//  1. Validates the quote is still valid (exists, not expired, not consumed)
//  2. Verifies wallet and network match the quote
//  3. Generates a unique intent_id and nonce
//  4. Builds a canonical signable payload for the wallet to sign
//  5. Persists the order intent with status "prepared"
//  6. Returns the intent ID and signable payload
func (s *CheckoutService) PrepareCheckout(ctx context.Context, req *model.PrepareRequest) (*model.PrepareResponse, error) {
	// SECURITY FIX: F-001-01 — Check for idempotent replay of prepare.
	if req.IdempotencyKey != "" {
		existingIntent, err := s.orderRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
		if err != nil {
			return nil, fmt.Errorf("prepare checkout: idempotency lookup failed: %w", err)
		}
		if existingIntent != nil {
			// Replay: return the existing intent without creating a new one.
			// Verify the existing intent matches the current request (wallet/quote must match).
			if existingIntent.QuoteID != req.QuoteID {
				return nil, fmt.Errorf("prepare checkout: idempotency key %s was used with a different quote_id (%s vs %s)",
					req.IdempotencyKey, existingIntent.QuoteID, req.QuoteID)
			}
			if existingIntent.Wallet != req.Wallet {
				return nil, fmt.Errorf("prepare checkout: idempotency key %s was used with a different wallet (%s vs %s)",
					req.IdempotencyKey, existingIntent.Wallet, req.Wallet)
			}

			// Reconstruct the signable payload from the stored intent.
			var signablePayload model.SignablePayload
			if err := json.Unmarshal(existingIntent.SignablePayload, &signablePayload); err != nil {
				return nil, fmt.Errorf("prepare checkout: failed to reconstruct signable payload for replay: %w", err)
			}

			return &model.PrepareResponse{
				OrderIntentID:   existingIntent.IntentID,
				QuoteID:         existingIntent.QuoteID,
				SignablePayload: &signablePayload,
			}, nil
		}
	}
	// Validate the quote.
	q, err := s.quoteService.ValidateQuote(ctx, req.QuoteID, req.Wallet)
	if err != nil {
		return nil, fmt.Errorf("prepare checkout: %w", err)
	}

	// Verify network matches.
	if q.Network != req.Network {
		return nil, fmt.Errorf("prepare checkout: network %s does not match quote network %s", req.Network, q.Network)
	}

	// Generate intent_id: "intent_" + 16 bytes random hex.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("prepare checkout: failed to generate intent id: %w", err)
	}
	intentID := fmt.Sprintf("intent_%s", hex.EncodeToString(b))

	// Generate nonce: 16 bytes random hex (32 chars, 128 bits).
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, fmt.Errorf("prepare checkout: failed to generate nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)

	// Build the canonical signable payload.
	payload := &model.SignablePayload{
		Domain:     s.domain,
		ShopID:     s.shopID,
		Network:    q.Network,
		Wallet:     q.Wallet,
		QuoteID:    q.QuoteID,
		TotalMinor: strconv.FormatInt(q.TotalMinor, 10),
		Currency:   q.Currency,
		ExpiresAt:  q.ExpiresAt.Format(time.RFC3339),
		Nonce:      nonce,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("prepare checkout: failed to marshal signable payload: %w", err)
	}

	// Persist the order intent.
	now := time.Now().UTC()
	noncePtr := &nonce

	var agentSessionID *string
	if req.AgentSessionID != "" {
		agentSessionID = &req.AgentSessionID
	}

	// SECURITY FIX: F-001-01 — Store idempotency key on intent for replay detection.
	var idempotencyKey *string
	if req.IdempotencyKey != "" {
		idempotencyKey = &req.IdempotencyKey
	}

	intent := &model.OrderIntent{
		IntentID:        intentID,
		QuoteID:         q.QuoteID,
		Wallet:          q.Wallet,
		Network:         q.Network,
		Currency:        q.Currency,
		TotalMinor:      q.TotalMinor,
		Status:          model.StatusPrepared,
		IdempotencyKey:  idempotencyKey,
		Nonce:           noncePtr,
		AgentSessionID:  agentSessionID,
		SignablePayload: payloadJSON,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.orderRepo.CreateIntent(ctx, intent); err != nil {
		return nil, fmt.Errorf("prepare checkout: failed to create intent: %w", err)
	}

	return &model.PrepareResponse{
		OrderIntentID:   intentID,
		QuoteID:         q.QuoteID,
		SignablePayload: payload,
	}, nil
}

// CommitCheckout finalizes the checkout with EdDSA signature verification, idempotency, and
// transactional state transitions.
//
// Full transaction boundary (demo.md section 11):
//  1. Compute request body hash for idempotency
//  2. Quick idempotency pre-check (outside tx): replay cached response or detect conflict
//  3. Validate order intent exists and is in "prepared" status
//  4. State machine validation: prepared -> committed
//  5. Validate quote (read, outside tx)
//  6. Wallet consistency check across quote, intent, and request
//  7. Full EdDSA (Ed25519) signature verification against the signable payload
//  8. BEGIN TRANSACTION
//     a. Lock quote (SELECT FOR UPDATE)
//     b. Re-validate quote within lock (not expired, not consumed, wallet match)
//     c. Lock order intent (SELECT FOR UPDATE)
//     d. Update intent status to "committed"
//     e. Mark quote consumed
//     f. Lock inventory rows (SELECT FOR UPDATE) and deduct stock
//     g. Write outbox event (order_committed)
//     h. Atomically lock idempotency key + save response (F-001-04 unified)
//  9. COMMIT
//  10. Return the CommitResponse
func (s *CheckoutService) CommitCheckout(ctx context.Context, req *model.CommitRequest, idempotencyKey string) (*model.CommitResponse, error) {
	// SECURITY FIX: F-001-03 — Apply a 30-second database timeout to the commit
	// flow to prevent long-running transactions from holding locks.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Step 1: Compute request body hash for idempotency tracking.
	bodyJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: failed to marshal request: %w", err)
	}
	bodyHash := fmt.Sprintf("%x", sha256.Sum256(bodyJSON))

	// Step 2: Quick idempotency pre-check (outside transaction).
	// 三路幂等解析（事务外快路径）：回放命中返回缓存响应 / 体哈希冲突返回 409 / 全新键则继续提交。
	// This handles the fast-path cases: replay (return cached response) and conflict (return 409).
	idCheck, err := CheckAndResolveIdempotency(ctx, s.orderRepo, idempotencyKey, req)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: idempotency check failed: %w", err)
	}
	switch idCheck.Result {
	case IdempotencyReplay:
		// Return the cached response directly.
		return idCheck.CachedResponse, nil
	case IdempotencyConflict:
		return nil, fmt.Errorf("commit checkout: %w", idCheck.ConflictError)
	case IdempotencyNew:
		// Proceed with the commit flow.
	}

	// Step 3: Validate the order intent (read, outside tx).
	intent, err := s.orderRepo.GetByIntentID(ctx, req.OrderIntentID)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: failed to retrieve intent: %w", err)
	}
	if intent == nil {
		return nil, fmt.Errorf("commit checkout: intent %s not found", req.OrderIntentID)
	}

	// Step 4: State machine validation — only prepared -> committed is allowed.
	// 状态机校验：仅允许 prepared -> committed，拦截重复提交或非法状态流转。
	if err := ValidateTransition(intent.Status, model.StatusCommitted); err != nil {
		return nil, fmt.Errorf("commit checkout: invalid state transition: %w", err)
	}

	// Step 5: Validate the quote (read, outside tx).
	q, err := s.quoteService.ValidateQuote(ctx, req.QuoteID, req.Wallet)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: %w", err)
	}

	// Step 6: Verify wallet consistency across quote, intent, and request.
	if q.Wallet != intent.Wallet {
		return nil, fmt.Errorf("commit checkout: quote wallet %s does not match intent wallet %s", q.Wallet, intent.Wallet)
	}
	if req.Wallet != q.Wallet {
		return nil, fmt.Errorf("commit checkout: request wallet %s does not match quote wallet %s", req.Wallet, q.Wallet)
	}

	// Step 7: Full EdDSA (Ed25519) signature verification.
	// 安全核心：校验用户钱包对 signable_payload 的 Ed25519 签名，确保交易意图由钱包私钥本人授权，防止伪造/篡改。
	if req.WalletSignature == "" {
		return nil, fmt.Errorf("commit checkout: wallet_signature is required")
	}
	if intent.SignablePayload == nil {
		return nil, fmt.Errorf("commit checkout: intent %s has no signable payload", req.OrderIntentID)
	}
	sigValid, err := VerifyEdDSASignature(intent.SignablePayload, req.Wallet, req.WalletSignature)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: signature verification failed: %w", err)
	}
	if !sigValid {
		return nil, fmt.Errorf("commit checkout: wallet signature is invalid")
	}

	// Step 8: Execute the commit within a single database transaction.
	// 一致性核心：报价锁定/消费、意图状态流转、库存扣减、Outbox 事件、幂等键保存全部在同一事务内完成，保证原子性。
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: recommendedIsolation()})
	if err != nil {
		return nil, fmt.Errorf("commit checkout: failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 8a. Lock the quote row (SELECT FOR UPDATE) and re-validate within the lock.
	lockedQuote, err := s.quoteService.LockAndValidateQuoteTx(ctx, tx, req.QuoteID, req.Wallet)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: quote lock failed: %w", err)
	}
	// lockedQuote is now used for inventory lock/deduct and outbox event payload.

	// 8b. Lock the order intent row (SELECT FOR UPDATE).
	lockedIntent, err := s.orderRepo.LockIntentForUpdate(ctx, tx, req.OrderIntentID)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: failed to lock intent: %w", err)
	}
	if lockedIntent == nil {
		return nil, fmt.Errorf("commit checkout: intent %s disappeared during commit", req.OrderIntentID)
	}

	// Re-verify intent status within the lock (prevent race).
	if lockedIntent.Status != model.StatusPrepared {
		return nil, fmt.Errorf("commit checkout: intent %s status changed to %s during commit", req.OrderIntentID, lockedIntent.Status)
	}

	// 8c. Update intent status to committed.
	if err := s.orderRepo.UpdateStatusWithTx(ctx, tx, req.OrderIntentID, model.StatusCommitted); err != nil {
		return nil, fmt.Errorf("commit checkout: failed to commit intent: %w", err)
	}

	// 8d. Mark the quote as consumed within the transaction.
	// 报价原子消费：consumed 为 false 说明并发请求已抢先消费，回滚以防重复下单/超卖。
	consumed, err := s.quoteService.MarkQuoteConsumedTx(ctx, tx, req.QuoteID)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: failed to mark quote consumed: %w", err)
	}
	if !consumed {
		return nil, fmt.Errorf("commit checkout: quote %s was already consumed (detected within transaction)", req.QuoteID)
	}

	// 8e. Lock inventory rows and deduct stock for each quote line.
	// 库存防超卖：逐行 SELECT FOR UPDATE 锁定 SKU 后扣减，扣减条件 WHERE stock >= qty。
	// Parse the quote lines to extract SKU IDs and quantities.
	var quoteLines []quoteModel.QuoteLine
	if err := json.Unmarshal(lockedQuote.Lines, &quoteLines); err != nil {
		return nil, fmt.Errorf("commit checkout: failed to parse quote lines: %w", err)
	}
	for _, line := range quoteLines {
		// Lock the SKU row (SELECT FOR UPDATE) to prevent concurrent inventory modifications.
		if _, err := s.skuRepo.LockSKUForUpdate(ctx, tx, line.SkuID); err != nil {
			return nil, fmt.Errorf("commit checkout: inventory lock %s: %w", line.SkuID, err)
		}
		// Deduct stock within the same transaction. Uses WHERE stock >= qty to prevent overselling.
		if err := s.skuRepo.DeductStockWithTx(ctx, tx, line.SkuID, line.Quantity); err != nil {
			return nil, fmt.Errorf("commit checkout: inventory deduct %s: %w", line.SkuID, err)
		}
	}

	// 8f. Write outbox event (order_committed) within the transaction.
	// Outbox 模式：事务内只写发件箱，不直接调用外部服务；事件随事务提交后才对投递器可见。
	// The event becomes visible to the outbox processor only after COMMIT.
	evtIDBytes := make([]byte, 16)
	if _, err := rand.Read(evtIDBytes); err != nil {
		return nil, fmt.Errorf("commit checkout: failed to generate event id: %w", err)
	}
	evtID := fmt.Sprintf("evt_%s", hex.EncodeToString(evtIDBytes))

	outboxPayload := map[string]interface{}{
		"order_id":        deriveOrderID(req.OrderIntentID),
		"order_intent_id": req.OrderIntentID,
		"quote_id":        req.QuoteID,
		"wallet":          req.Wallet,
		"total_minor":     lockedQuote.TotalMinor,
		"currency":        lockedQuote.Currency,
		"line_count":      len(quoteLines),
	}
	outboxPayloadJSON, err := json.Marshal(outboxPayload)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: failed to marshal outbox payload: %w", err)
	}

	outboxEvent := &repository.OutboxEvent{
		EventID:       evtID,
		EventType:     "order_committed",
		AggregateType: "order",
		AggregateID:   req.OrderIntentID,
		Payload:       json.RawMessage(outboxPayloadJSON),
	}
	if err := s.outboxRepo.InsertWithTx(ctx, tx, outboxEvent); err != nil {
		return nil, fmt.Errorf("commit checkout: outbox insert: %w", err)
	}

	// 8g. Build the commit response.
	orderID := deriveOrderID(req.OrderIntentID)
	now := time.Now().UTC()
	commitResp := &model.CommitResponse{
		OrderID:       orderID,
		Status:        model.StatusCommitted,
		OrderIntentID: req.OrderIntentID,
		CreatedAt:     now.Format(time.RFC3339),
	}

	// 8h. Atomically lock the idempotency key and save the response.
	// SECURITY FIX: F-001-04 — LockAndSaveIdempotencyKeyTx replaces the
	// two-step LockIdempotencyKeyTx + SaveIdempotencyResponseTx pattern
	// with a single atomic INSERT, eliminating the window where a crash
	// leaves the key locked with an empty response body.
	respJSON, err := json.Marshal(commitResp)
	if err != nil {
		return nil, fmt.Errorf("commit checkout: failed to marshal response: %w", err)
	}
	if err := s.orderRepo.LockAndSaveIdempotencyKeyTx(ctx, tx, idempotencyKey, bodyHash, 200, string(respJSON)); err != nil {
		if _, ok := err.(*repository.IdempotencyConflictError); ok {
			// Re-check what the existing key holds (concurrent request won).
			cached, checkErr := s.orderRepo.CheckIdempotencyKey(ctx, idempotencyKey, bodyHash)
			if checkErr == nil && cached != nil {
				var commitResp model.CommitResponse
				if json.Unmarshal([]byte(cached.ResponseBody), &commitResp) == nil {
					return &commitResp, nil
				}
			}
			return nil, fmt.Errorf("commit checkout: concurrent conflict on idempotency key %s", idempotencyKey)
		}
		return nil, fmt.Errorf("commit checkout: failed to save idempotency response: %w", err)
	}

	// Step 9: Commit the transaction.
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit checkout: transaction commit failed: %w", err)
	}

	// Step 10: Return the response.
	return commitResp, nil
}

// deriveOrderID converts an intent_id (format: "intent_<hex>") into an order_id ("order_<hex>").
func deriveOrderID(intentID string) string {
	if len(intentID) > 7 && intentID[:7] == "intent_" {
		return "order_" + intentID[7:]
	}
	return "order_" + intentID
}
