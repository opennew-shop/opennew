// Package service 实现 mint 服务的业务逻辑层。
// 它涵盖：铸币（充值确认→影子账本入账，9 状态状态机）、赎回（锁定余额→销毁→
// 打款→失败释放，8 状态状态机），以及储备对账（校验
// total_liability + pending_redemption <= confirmed_reserve 不变式）。
// 配合 ledger 双分录服务、风控与储备覆盖检查以及 SERIALIZABLE 事务保证一致性。
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	ledgersvc "github.com/ancf-commerce/ancf/services/ledger/service"
	"github.com/ancf-commerce/ancf/services/mint/internal/model"
	"github.com/ancf-commerce/ancf/services/mint/internal/repository"
)

// MintService provides business logic for vUSDC mint (deposit→credit) operations.
//
// Phase 3 uses the shadow-ledger vUSDC model (demo.md §7.1):
//   - No on-chain minting.
//   - After confirming a reserve deposit, the service directly calls
//     ledger.MintCredit to increase the user's vUSDC available balance.
//   - reserve_liability and reserve_asset stay in balance.
//   - Risk checks execute before crediting.
//
// All mutation methods use caller-managed transactions for atomicity.
type MintService struct {
	mintRepo      *repository.MintRepository
	ledgerService *ledgersvc.LedgerService
	auditRepo     *repository.MintRepository // audit events through mintRepo for now; nil = skip
	db            *sql.DB
}

// NewMintService creates a new MintService.
// auditRepo may be nil — audit events will be written through mintRepo if non-nil.
func NewMintService(
	db *sql.DB,
	mintRepo *repository.MintRepository,
	ledgerService *ledgersvc.LedgerService,
) *MintService {
	return &MintService{
		mintRepo:      mintRepo,
		ledgerService: ledgerService,
		auditRepo:     mintRepo, // reuse mintRepo for audit writes in MVP
		db:            db,
	}
}

// CreateDepositIntent creates a deposit intent, returning the reserve address
// and a unique deposit_intent_id for the user to send funds to.
//
// Flow:
//  1. Generate a unique deposit_intent_id.
//  2. Look up the asset by symbol + network.
//  3. Look up the reserve account for that network + asset symbol.
//  4. INSERT into mint_requests with status=created and amount_minor=0
//     (amount is filled during ConfirmDeposit).
//  5. Return deposit_intent_id + reserve_address + memo.
//
// Note: The amount_minor is not known at intent-creation time because the user
// has not yet sent funds. The intent reserves a request_id so the chain-adapter
// can match the incoming deposit. A zero-amount intent is valid; the amount is
// set during ConfirmDeposit.
func (s *MintService) CreateDepositIntent(ctx context.Context, req *model.DepositIntentRequest) (*model.DepositIntentResponse, error) {
	// Validate inputs.
	if req.Wallet == "" {
		return nil, fmt.Errorf("create deposit intent: wallet is required")
	}
	if req.Network == "" {
		return nil, fmt.Errorf("create deposit intent: network is required")
	}
	if req.AssetSymbol == "" {
		return nil, fmt.Errorf("create deposit intent: asset_symbol is required")
	}

	// Look up the active asset.
	asset, err := s.mintRepo.GetAsset(ctx, req.AssetSymbol, req.Network)
	if err != nil {
		return nil, fmt.Errorf("create deposit intent: %w", err)
	}

	// Look up the reserve account for this network + asset.
	reserve, err := s.mintRepo.GetReserveAccount(ctx, req.Network, req.AssetSymbol)
	if err != nil {
		return nil, fmt.Errorf("create deposit intent: %w", err)
	}

	// Generate a unique deposit_intent_id.
	depositIntentID := repository.GenerateID("di_")

	// Create the mint request with status=created and zero amount.
	// The chain-adapter will update the amount when confirming the deposit.
	mintReq := &model.MintRequest{
		RequestID:   depositIntentID,
		Wallet:      req.Wallet,
		AssetID:     asset.ID,
		AmountMinor: 0, // unknown until deposit confirmation
		Status:      model.MintStatusCreated,
	}

	// Use a short-lived transaction for the insert.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create deposit intent: begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := s.mintRepo.CreateMintRequest(ctx, tx, mintReq); err != nil {
		return nil, fmt.Errorf("create deposit intent: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create deposit intent: commit: %w", err)
	}

	// Build a memo that helps the chain-adapter match the deposit.
	memo := fmt.Sprintf("ancf-deposit:%s:%s", req.AssetSymbol, depositIntentID)

	return &model.DepositIntentResponse{
		DepositIntentID: depositIntentID,
		ReserveAddress:  reserve.Address,
		Memo:            memo,
	}, nil
}

// ConfirmDeposit processes a confirmed deposit from the chain-adapter.
//
// Idempotency: The same deposit_tx_id (on-chain transaction hash) is only
// processed once. Re-entrant calls with an already-processed deposit_tx_id
// return nil (success) without side effects. This is critical for the
// cross-service outbox pattern where the chain-adapter's DepositProcessor
// may retry ConfirmDeposit calls.
//
// Full transaction boundary (shadow-ledger mode):
//  0. Idempotency check: SELECT mint_requests WHERE reserve_deposit_tx_id = depositTxID
//     If found and in a terminal state → return nil (already processed)
//  1. BEGIN TRANSACTION
//  2. Lock mint_request FOR UPDATE
//  3. Validate state transition: created → deposit_confirmed
//  4. Update reserve_deposit_tx_id
//  5. Risk check against mint policy (amount, wallet, daily/per-wallet limits)
//  6. Transition: deposit_confirmed → risk_checking → approved (auto-approve if below threshold)
//  7. Shadow-ledger: skip on-chain mint, go directly to mint_submitted → minted
//  8. Call ledgerService.MintCredit(tx, wallet, amount, currency, depositTxID)
//     This writes the double entry:
//       debit reserve_asset / credit reserve_liability
//       debit reserve_liability / credit user_available
//  9. Update mint_request status → credited
//  10. INSERT audit_log event
//  11. COMMIT
func (s *MintService) ConfirmDeposit(ctx context.Context, requestID string, depositTxID string, amountMinor int64) error {
	if requestID == "" {
		return fmt.Errorf("confirm deposit: request_id is required")
	}
	if depositTxID == "" {
		return fmt.Errorf("confirm deposit: deposit_tx_id is required")
	}
	if amountMinor <= 0 {
		return fmt.Errorf("confirm deposit: amount_minor must be > 0, got %d", amountMinor)
	}

	// Step 1: BEGIN TRANSACTION
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("confirm deposit: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Step 0: Idempotency check — same deposit_tx_id must only credit once.
	existing, err := s.mintRepo.GetByDepositTxIDForUpdate(ctx, tx, depositTxID)
	if err != nil {
		return fmt.Errorf("confirm deposit: idempotency check: %w", err)
	}
	if existing != nil {
		if existing.RequestID == requestID && existing.Status == model.MintStatusCredited {
			return nil
		}
		return fmt.Errorf("confirm deposit: deposit_tx_id %s is already attached to request %s (status=%s)",
			depositTxID, existing.RequestID, existing.Status)
	}

	// Step 2: Lock mint_request FOR UPDATE
	locked, err := s.mintRepo.LockForUpdate(ctx, tx, requestID)
	if err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}

	// Step 3: Validate state transition created → deposit_confirmed
	if err := model.ValidateMintTransition(locked.Status, model.MintStatusDepositConfirmed); err != nil {
		return fmt.Errorf("confirm deposit: invalid transition for %s (current=%s): %w", requestID, locked.Status, err)
	}

	asset, err := s.mintRepo.GetAssetByID(ctx, locked.AssetID)
	if err != nil {
		return fmt.Errorf("confirm deposit: asset lookup: %w", err)
	}

	reserve, err := s.mintRepo.GetReserveAccountForUpdate(ctx, tx, asset.Network, asset.Symbol)
	if err != nil {
		return fmt.Errorf("confirm deposit: reserve lookup: %w", err)
	}

	proof, err := s.mintRepo.GetFinalizedDepositProofForUpdate(ctx, tx, asset.Network, depositTxID)
	if err != nil {
		return fmt.Errorf("confirm deposit: chain proof: %w", err)
	}
	if proof.TxHash != depositTxID {
		return fmt.Errorf("confirm deposit: proof tx mismatch: expected %s, got %s", depositTxID, proof.TxHash)
	}
	if proof.Network != asset.Network {
		return fmt.Errorf("confirm deposit: proof network mismatch: expected %s, got %s", asset.Network, proof.Network)
	}
	if proof.AssetSymbol != asset.Symbol {
		return fmt.Errorf("confirm deposit: proof asset mismatch: expected %s, got %s", asset.Symbol, proof.AssetSymbol)
	}
	if asset.MintAddress == nil || *asset.MintAddress == "" {
		return fmt.Errorf("confirm deposit: asset %s/%s has no configured mint address", asset.Network, asset.Symbol)
	}
	if proof.MintAddress == "" {
		return fmt.Errorf("confirm deposit: proof missing mint address")
	}
	if proof.MintAddress != *asset.MintAddress {
		return fmt.Errorf("confirm deposit: proof mint mismatch: expected %s, got %s", *asset.MintAddress, proof.MintAddress)
	}
	if proof.AmountMinor != amountMinor {
		return fmt.Errorf("confirm deposit: proof amount mismatch: expected %d, got %d", amountMinor, proof.AmountMinor)
	}
	if proof.ToAddress != reserve.Address {
		return fmt.Errorf("confirm deposit: proof reserve address mismatch")
	}
	if proof.FromAddress != locked.Wallet {
		return fmt.Errorf("confirm deposit: proof wallet mismatch: expected %s, got %s", locked.Wallet, proof.FromAddress)
	}
	if proof.DepositIntentID != requestID {
		return fmt.Errorf("confirm deposit: proof deposit intent mismatch: expected %s, got %s", requestID, proof.DepositIntentID)
	}

	// If the amount was zero at intent creation, set it now.
	// If it was pre-set, verify it matches.
	if locked.AmountMinor == 0 {
		locked.AmountMinor = amountMinor
	} else if locked.AmountMinor != amountMinor {
		return fmt.Errorf("confirm deposit: amount mismatch for %s: expected %d, got %d",
			requestID, locked.AmountMinor, amountMinor)
	}

	// Step 4: Get the mint policy for risk checking.
	policy, err := s.mintRepo.GetMintPolicyForUpdate(ctx, tx, locked.AssetID)
	if err != nil {
		// If policy lookup fails, fail the mint request.
		s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusFailed)
		return fmt.Errorf("confirm deposit: mint policy lookup: %w", err)
	}

	// Step 5: Risk check — validate against policy limits.
	today := time.Now().UTC().Format("2006-01-02")
	if err := s.mintRepo.CheckDailyLimitForUpdate(ctx, tx, locked.Wallet, amountMinor, today, policy); err != nil {
		s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusFailed)
		return fmt.Errorf("confirm deposit: risk check failed: %w", err)
	}

	if err := s.mintRepo.CheckReserveCoverageForUpdate(ctx, tx, asset.Symbol, reserve, amountMinor); err != nil {
		s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusFailed)
		return fmt.Errorf("confirm deposit: reserve check failed: %w", err)
	}

	if err := s.mintRepo.UpdateDepositDetails(ctx, tx, requestID, depositTxID, amountMinor); err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}

	// Set status to deposit_confirmed.
	if err := s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusDepositConfirmed); err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}

	// Transition to risk_checking (audit trail).
	if err := s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusRiskChecking); err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}

	// Determine if manual approval is required.
	// Shadow-ledger MVP: auto-approve below the manual-approval threshold and
	// fail closed above it; the Phase 4 on-chain multisig path is not wired into
	// this shadow-ledger credit path.
	if amountMinor >= policy.RequireManualApprovalAboveMinor && policy.RequireManualApprovalAboveMinor > 0 {
		// Large amount requires manual approval. In MVP, fail fast and require admin.
		if err := s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusFailed); err != nil {
			return fmt.Errorf("confirm deposit: %w", err)
		}
		return fmt.Errorf("confirm deposit: amount %d exceeds manual approval threshold %d for request %s",
			amountMinor, policy.RequireManualApprovalAboveMinor, requestID)
	}

	// Auto-approve.
	if err := s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusApproved); err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}

	// Step 6: Shadow ledger mode — skip on-chain mint, mark as submitted and minted.
	if err := s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusMintSubmitted); err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}
	if err := s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusMinted); err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}

	// Step 7: Call ledger.MintCredit to credit vUSDC to the user's available balance.
	// Double entry:
	//   debit  reserve_asset     / credit reserve_liability
	//   debit  reserve_liability / credit user_available
	currency := asset.Symbol
	if err := s.ledgerService.MintCredit(ctx, tx, locked.Wallet, amountMinor, currency, depositTxID); err != nil {
		if statusErr := s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusFailed); statusErr != nil {
			return fmt.Errorf("confirm deposit: mint credit failed: %w; status update also failed: %v", err, statusErr)
		}
		return fmt.Errorf("confirm deposit: mint credit: %w", err)
	}

	// Step 8: Update status to credited.
	if err := s.mintRepo.UpdateStatus(ctx, tx, requestID, model.MintStatusCredited); err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}

	// Step 9: Write audit log.
	auditEvent := &model.AuditEvent{
		EventID:      repository.GenerateID("audit_"),
		EventType:    "mint_deposit_confirmed",
		ActorType:    "system",
		ActorID:      "mint-service",
		ResourceType: "mint_request",
		ResourceID:   requestID,
		Action:       "confirm_deposit",
		Details: mustMarshalJSON(map[string]interface{}{
			"wallet":        locked.Wallet,
			"amount_minor":  amountMinor,
			"currency":      currency,
			"deposit_tx_id": depositTxID,
			"asset_id":      locked.AssetID,
			"final_status":  model.MintStatusCredited,
		}),
	}
	if err := s.mintRepo.InsertAuditLog(ctx, tx, auditEvent); err != nil {
		return fmt.Errorf("confirm deposit: audit log: %w", err)
	}

	// Step 10: Update daily totals (derived from mint_requests aggregation in MVP).
	if err := s.mintRepo.IncrementDailyTotal(ctx, tx, locked.Wallet, amountMinor, today); err != nil {
		return fmt.Errorf("confirm deposit: daily total: %w", err)
	}

	// Step 11: COMMIT
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("confirm deposit: commit: %w", err)
	}

	return nil
}

// GetRequest retrieves the current state of a mint request by request ID.
func (s *MintService) GetRequest(ctx context.Context, requestID string) (*model.MintStatusResponse, error) {
	req, err := s.mintRepo.GetByRequestID(ctx, requestID)
	if err != nil {
		return nil, fmt.Errorf("get mint request: %w", err)
	}
	if req == nil {
		return nil, nil
	}

	// Resolve asset symbol for display.
	assetSymbol := "vUSDC" // default
	if asset, assetErr := s.mintRepo.GetAssetByID(ctx, req.AssetID); assetErr == nil {
		assetSymbol = asset.Symbol
	}

	return &model.MintStatusResponse{
		RequestID:          req.RequestID,
		Wallet:             req.Wallet,
		AssetSymbol:        assetSymbol,
		AmountMinor:        req.AmountMinor,
		Status:             req.Status,
		RiskScore:          req.RiskScore,
		ReserveDepositTxID: req.ReserveDepositTxID,
		ChainMintTxID:      req.ChainMintTxID,
		CreatedAt:          req.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          req.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// GetReserveInfo retrieves the reserve account information for a network and asset symbol.
func (s *MintService) GetReserveInfo(ctx context.Context, network, assetSymbol string) (*model.ReserveInfoResponse, error) {
	ra, err := s.mintRepo.GetReserveAccount(ctx, network, assetSymbol)
	if err != nil {
		return nil, fmt.Errorf("get reserve info: %w", err)
	}

	return &model.ReserveInfoResponse{
		Network:               ra.Network,
		AssetSymbol:           ra.AssetSymbol,
		ReserveAddress:        ra.Address,
		ConfirmedBalanceMinor: ra.ConfirmedBalanceMinor,
		PendingBalanceMinor:   ra.PendingBalanceMinor,
	}, nil
}

// mustMarshalJSON marshals v to JSON or returns an empty JSON object on error.
func mustMarshalJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(data)
}
