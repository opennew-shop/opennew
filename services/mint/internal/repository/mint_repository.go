// Package repository 实现 mint 服务的持久化层。
// 它封装对 mint_requests、redemption_requests、reserve_accounts、mint_policies、
// chain_txs、ledger_entries、audit_log 等表的读写；变更类操作要求调用方传入
// 显式事务（*sql.Tx）以掌控事务边界，并借助 SELECT ... FOR UPDATE 行级锁
// 支撑铸币/赎回的并发安全与防双花。
package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ancf-commerce/ancf/services/mint/internal/model"
)

// MintRepository handles all mint persistence operations.
// All mutation methods require an explicit *sql.Tx to ensure callers
// control the transaction boundary.
type MintRepository struct {
	db *sql.DB
}

// NewMintRepository creates a new MintRepository backed by the given *sql.DB.
func NewMintRepository(db *sql.DB) *MintRepository {
	return &MintRepository{db: db}
}

// CreateMintRequest inserts a new mint request inside an existing transaction.
func (r *MintRepository) CreateMintRequest(ctx context.Context, tx *sql.Tx, req *model.MintRequest) error {
	stmt := `INSERT INTO mint_requests
		(request_id, wallet, asset_id, reserve_deposit_tx_id, amount_minor, status, risk_score, approval_id, chain_mint_tx_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	if req.RequestID == "" {
		return fmt.Errorf("create mint request: request_id is required")
	}
	if req.AmountMinor < 0 {
		return fmt.Errorf("create mint request: amount_minor must be >= 0, got %d", req.AmountMinor)
	}
	if req.Wallet == "" {
		return fmt.Errorf("create mint request: wallet is required")
	}

	now := time.Now().UTC()
	req.CreatedAt = now
	req.UpdatedAt = now

	_, err := tx.ExecContext(ctx, stmt,
		req.RequestID,
		req.Wallet,
		req.AssetID,
		req.ReserveDepositTxID,
		req.AmountMinor,
		req.Status,
		req.RiskScore,
		req.ApprovalID,
		req.ChainMintTxID,
		req.CreatedAt,
		req.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create mint request %s: %w", req.RequestID, err)
	}
	return nil
}

// GetByRequestID retrieves a mint request by its request_id (outside a transaction).
func (r *MintRepository) GetByRequestID(ctx context.Context, requestID string) (*model.MintRequest, error) {
	query := `SELECT id, request_id, wallet, asset_id, reserve_deposit_tx_id,
		amount_minor, status, risk_score, approval_id, chain_mint_tx_id,
		created_at, updated_at
		FROM mint_requests WHERE request_id = $1`

	req := &model.MintRequest{}
	err := r.db.QueryRowContext(ctx, query, requestID).Scan(
		&req.ID, &req.RequestID, &req.Wallet, &req.AssetID,
		&req.ReserveDepositTxID, &req.AmountMinor, &req.Status,
		&req.RiskScore, &req.ApprovalID, &req.ChainMintTxID,
		&req.CreatedAt, &req.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mint request %s: %w", requestID, err)
	}
	return req, nil
}

// GetByDepositTxID retrieves a mint request by its reserve_deposit_tx_id.
// This is used for idempotency: the same on-chain deposit must only be
// credited once to a user's wallet, regardless of how many times
// ConfirmDeposit is called with that deposit_tx_id.
// Returns nil, nil when no matching request is found.
func (r *MintRepository) GetByDepositTxID(ctx context.Context, txID string) (*model.MintRequest, error) {
	query := `SELECT id, request_id, wallet, asset_id, reserve_deposit_tx_id,
		amount_minor, status, risk_score, approval_id, chain_mint_tx_id,
		created_at, updated_at
		FROM mint_requests WHERE reserve_deposit_tx_id = $1`

	req := &model.MintRequest{}
	err := r.db.QueryRowContext(ctx, query, txID).Scan(
		&req.ID, &req.RequestID, &req.Wallet, &req.AssetID,
		&req.ReserveDepositTxID, &req.AmountMinor, &req.Status,
		&req.RiskScore, &req.ApprovalID, &req.ChainMintTxID,
		&req.CreatedAt, &req.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mint request by deposit tx id %s: %w", txID, err)
	}
	return req, nil
}

// GetByDepositTxIDForUpdate locks a mint request by reserve_deposit_tx_id.
func (r *MintRepository) GetByDepositTxIDForUpdate(ctx context.Context, tx *sql.Tx, txID string) (*model.MintRequest, error) {
	query := `SELECT id, request_id, wallet, asset_id, reserve_deposit_tx_id,
		amount_minor, status, risk_score, approval_id, chain_mint_tx_id,
		created_at, updated_at
		FROM mint_requests WHERE reserve_deposit_tx_id = $1 FOR UPDATE`

	req := &model.MintRequest{}
	err := tx.QueryRowContext(ctx, query, txID).Scan(
		&req.ID, &req.RequestID, &req.Wallet, &req.AssetID,
		&req.ReserveDepositTxID, &req.AmountMinor, &req.Status,
		&req.RiskScore, &req.ApprovalID, &req.ChainMintTxID,
		&req.CreatedAt, &req.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock mint request by deposit tx id %s: %w", txID, err)
	}
	return req, nil
}

// UpdateStatus updates the status of a mint request inside an existing transaction.
func (r *MintRepository) UpdateStatus(ctx context.Context, tx *sql.Tx, requestID string, status string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE mint_requests SET status = $1, updated_at = $2 WHERE request_id = $3`,
		status, time.Now().UTC(), requestID,
	)
	if err != nil {
		return fmt.Errorf("update mint status %s to %s: %w", requestID, status, err)
	}
	return nil
}

// UpdateDepositDetails sets the finalized deposit tx and amount inside a transaction.
func (r *MintRepository) UpdateDepositDetails(ctx context.Context, tx *sql.Tx, requestID string, depositTxID string, amountMinor int64) error {
	if amountMinor <= 0 {
		return fmt.Errorf("update deposit details: amount_minor must be > 0, got %d", amountMinor)
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE mint_requests
		 SET reserve_deposit_tx_id = $1, amount_minor = $2, updated_at = $3
		 WHERE request_id = $4`,
		depositTxID, amountMinor, time.Now().UTC(), requestID,
	)
	if err != nil {
		return fmt.Errorf("update deposit details for %s: %w", requestID, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("update deposit details for %s: request not found", requestID)
	}
	return nil
}

// LockForUpdate acquires a row-level lock on a mint request inside a transaction
// and returns the locked row. This serialises concurrent operations on the same request.
func (r *MintRepository) LockForUpdate(ctx context.Context, tx *sql.Tx, requestID string) (*model.MintRequest, error) {
	query := `SELECT id, request_id, wallet, asset_id, reserve_deposit_tx_id,
		amount_minor, status, risk_score, approval_id, chain_mint_tx_id,
		created_at, updated_at
		FROM mint_requests WHERE request_id = $1 FOR UPDATE`

	req := &model.MintRequest{}
	err := tx.QueryRowContext(ctx, query, requestID).Scan(
		&req.ID, &req.RequestID, &req.Wallet, &req.AssetID,
		&req.ReserveDepositTxID, &req.AmountMinor, &req.Status,
		&req.RiskScore, &req.ApprovalID, &req.ChainMintTxID,
		&req.CreatedAt, &req.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("mint request %s not found for lock", requestID)
	}
	if err != nil {
		return nil, fmt.Errorf("lock mint request %s: %w", requestID, err)
	}
	return req, nil
}

// GetReserveAccount retrieves the reserve account for a given network and asset symbol.
func (r *MintRepository) GetReserveAccount(ctx context.Context, network, assetSymbol string) (*model.ReserveAccount, error) {
	query := `SELECT id, network, asset_symbol, address,
		confirmed_balance_minor, pending_balance_minor,
		last_reconciled_at, created_at, updated_at
		FROM reserve_accounts WHERE network = $1 AND asset_symbol = $2`

	ra := &model.ReserveAccount{}
	err := r.db.QueryRowContext(ctx, query, network, assetSymbol).Scan(
		&ra.ID, &ra.Network, &ra.AssetSymbol, &ra.Address,
		&ra.ConfirmedBalanceMinor, &ra.PendingBalanceMinor,
		&ra.LastReconciledAt, &ra.CreatedAt, &ra.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("reserve account not found for network=%s asset=%s", network, assetSymbol)
	}
	if err != nil {
		return nil, fmt.Errorf("get reserve account network=%s asset=%s: %w", network, assetSymbol, err)
	}
	return ra, nil
}

// GetReserveAccountForUpdate locks the reserve account row for mint reserve checks.
func (r *MintRepository) GetReserveAccountForUpdate(ctx context.Context, tx *sql.Tx, network, assetSymbol string) (*model.ReserveAccount, error) {
	query := `SELECT id, network, asset_symbol, address,
		confirmed_balance_minor, pending_balance_minor,
		last_reconciled_at, created_at, updated_at
		FROM reserve_accounts WHERE network = $1 AND asset_symbol = $2 FOR UPDATE`

	ra := &model.ReserveAccount{}
	err := tx.QueryRowContext(ctx, query, network, assetSymbol).Scan(
		&ra.ID, &ra.Network, &ra.AssetSymbol, &ra.Address,
		&ra.ConfirmedBalanceMinor, &ra.PendingBalanceMinor,
		&ra.LastReconciledAt, &ra.CreatedAt, &ra.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("reserve account not found for network=%s asset=%s", network, assetSymbol)
	}
	if err != nil {
		return nil, fmt.Errorf("lock reserve account network=%s asset=%s: %w", network, assetSymbol, err)
	}
	return ra, nil
}

// GetAsset retrieves an asset by symbol and network.
func (r *MintRepository) GetAsset(ctx context.Context, symbol, network string) (*model.Asset, error) {
	query := `SELECT id, symbol, decimals, asset_type, network, mint_address, status
		FROM assets WHERE symbol = $1 AND network = $2 AND status = 'active'`

	a := &model.Asset{}
	err := r.db.QueryRowContext(ctx, query, symbol, network).Scan(
		&a.ID, &a.Symbol, &a.Decimals, &a.AssetType, &a.Network, &a.MintAddress, &a.Status,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("active asset not found for symbol=%s network=%s", symbol, network)
	}
	if err != nil {
		return nil, fmt.Errorf("get asset symbol=%s network=%s: %w", symbol, network, err)
	}
	return a, nil
}

// GetAssetByID retrieves an asset by its primary key ID.
func (r *MintRepository) GetAssetByID(ctx context.Context, assetID int64) (*model.Asset, error) {
	query := `SELECT id, symbol, decimals, asset_type, network, mint_address, status
		FROM assets WHERE id = $1`

	a := &model.Asset{}
	err := r.db.QueryRowContext(ctx, query, assetID).Scan(
		&a.ID, &a.Symbol, &a.Decimals, &a.AssetType, &a.Network, &a.MintAddress, &a.Status,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("asset not found for id=%d", assetID)
	}
	if err != nil {
		return nil, fmt.Errorf("get asset id=%d: %w", assetID, err)
	}
	return a, nil
}

// GetMintPolicy retrieves the active mint policy for a given asset ID.
func (r *MintRepository) GetMintPolicy(ctx context.Context, assetID int64) (*model.MintPolicy, error) {
	query := `SELECT id, asset_id, daily_mint_limit_minor, per_wallet_limit_minor,
		require_manual_approval_above_minor, status, created_at, updated_at
		FROM mint_policies WHERE asset_id = $1 AND status = 'active'`

	p := &model.MintPolicy{}
	err := r.db.QueryRowContext(ctx, query, assetID).Scan(
		&p.ID, &p.AssetID, &p.DailyMintLimitMinor, &p.PerWalletLimitMinor,
		&p.RequireManualApprovalAboveMinor, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no active mint policy for asset_id=%d", assetID)
	}
	if err != nil {
		return nil, fmt.Errorf("get mint policy asset_id=%d: %w", assetID, err)
	}
	return p, nil
}

// GetMintPolicyForUpdate locks the mint policy row inside a transaction.
func (r *MintRepository) GetMintPolicyForUpdate(ctx context.Context, tx *sql.Tx, assetID int64) (*model.MintPolicy, error) {
	query := `SELECT id, asset_id, daily_mint_limit_minor, per_wallet_limit_minor,
		require_manual_approval_above_minor, status, created_at, updated_at
		FROM mint_policies WHERE asset_id = $1 AND status = 'active' FOR UPDATE`

	p := &model.MintPolicy{}
	err := tx.QueryRowContext(ctx, query, assetID).Scan(
		&p.ID, &p.AssetID, &p.DailyMintLimitMinor, &p.PerWalletLimitMinor,
		&p.RequireManualApprovalAboveMinor, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no active mint policy for asset_id=%d", assetID)
	}
	if err != nil {
		return nil, fmt.Errorf("lock mint policy asset_id=%d: %w", assetID, err)
	}
	return p, nil
}

// CheckDailyLimitForUpdate enforces mint limits inside the caller's transaction.
func (r *MintRepository) CheckDailyLimitForUpdate(ctx context.Context, tx *sql.Tx, wallet string, newAmountMinor int64, date string, policy *model.MintPolicy) error {
	dailyQuery := `SELECT COALESCE(SUM(amount_minor), 0)
		FROM mint_requests
		WHERE wallet = $1
		  AND created_at::date = $2::date
		  AND status NOT IN ('cancelled', 'failed')`

	var dailyTotal int64
	if err := tx.QueryRowContext(ctx, dailyQuery, wallet, date).Scan(&dailyTotal); err != nil {
		return fmt.Errorf("check daily limit: %w", err)
	}
	if dailyTotal+newAmountMinor > policy.DailyMintLimitMinor {
		return fmt.Errorf("daily mint limit exceeded: wallet %s daily total %d + new %d > limit %d",
			wallet, dailyTotal, newAmountMinor, policy.DailyMintLimitMinor)
	}

	lifetimeQuery := `SELECT COALESCE(SUM(amount_minor), 0)
		FROM mint_requests
		WHERE wallet = $1
		  AND status NOT IN ('cancelled', 'failed')`

	var lifetimeTotal int64
	if err := tx.QueryRowContext(ctx, lifetimeQuery, wallet).Scan(&lifetimeTotal); err != nil {
		return fmt.Errorf("check per-wallet limit: %w", err)
	}
	if lifetimeTotal+newAmountMinor > policy.PerWalletLimitMinor {
		return fmt.Errorf("per-wallet mint limit exceeded: wallet %s lifetime total %d + new %d > limit %d",
			wallet, lifetimeTotal, newAmountMinor, policy.PerWalletLimitMinor)
	}

	return nil
}

// GetFinalizedDepositProofForUpdate locks and validates a finalized chain deposit row.
func (r *MintRepository) GetFinalizedDepositProofForUpdate(ctx context.Context, tx *sql.Tx, network, depositTxID string) (*model.ChainDepositProof, error) {
	var txType string
	var status string
	var confirmations int
	var rawJSON []byte
	err := tx.QueryRowContext(ctx, `
		SELECT tx_type, status, confirmations, raw_json
		FROM chain_txs
		WHERE network = $1 AND tx_hash = $2
		FOR UPDATE
	`, network, depositTxID).Scan(&txType, &status, &confirmations, &rawJSON)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deposit proof not found for network=%s tx=%s", network, depositTxID)
	}
	if err != nil {
		return nil, fmt.Errorf("lock deposit proof %s/%s: %w", network, depositTxID, err)
	}
	if txType != "deposit" {
		return nil, fmt.Errorf("chain tx %s is type %s, not deposit", depositTxID, txType)
	}
	if status != "finalized" || confirmations < 32 {
		return nil, fmt.Errorf("deposit tx %s is not finalized: status=%s confirmations=%d", depositTxID, status, confirmations)
	}
	if len(rawJSON) == 0 {
		return nil, fmt.Errorf("deposit tx %s has no raw proof payload", depositTxID)
	}

	var proof model.ChainDepositProof
	if err := json.Unmarshal(rawJSON, &proof); err != nil {
		return nil, fmt.Errorf("parse deposit proof %s: %w", depositTxID, err)
	}
	return &proof, nil
}

// CheckReserveCoverageForUpdate enforces confirmed reserves before mint credit.
func (r *MintRepository) CheckReserveCoverageForUpdate(ctx context.Context, tx *sql.Tx, assetSymbol string, reserve *model.ReserveAccount, newAmountMinor int64) error {
	var internalLiability int64
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(
			CASE WHEN credit_account IN ('user_available', 'user_pending', 'merchant_pending', 'merchant_settled', 'redemption_pending') THEN amount_minor
			     WHEN debit_account IN ('user_available', 'user_pending', 'merchant_pending', 'merchant_settled', 'redemption_pending') THEN -amount_minor
			     ELSE 0 END
		), 0)
		FROM ledger_entries
		WHERE currency = $1
	`, assetSymbol).Scan(&internalLiability)
	if err != nil {
		return fmt.Errorf("reserve coverage: compute internal liability: %w", err)
	}
	if internalLiability+newAmountMinor > reserve.ConfirmedBalanceMinor {
		return fmt.Errorf("reserve coverage exceeded: liability %d + new %d > confirmed reserve %d",
			internalLiability, newAmountMinor, reserve.ConfirmedBalanceMinor)
	}
	return nil
}

// IncrementDailyTotal is a no-op in the shadow-ledger implementation because
// daily totals are derived by aggregating the mint_requests table rather than
// maintained in a separate daily_totals table. The CheckDailyLimit method
// queries mint_requests directly.
//
// When a dedicated daily_totals table is introduced (e.g. for high-throughput
// production), this method should UPSERT the running totals atomically within
// the same transaction.
func (r *MintRepository) IncrementDailyTotal(ctx context.Context, tx *sql.Tx, wallet string, amountMinor int64, date string) error {
	// Shadow-ledger MVP: daily totals are derived from mint_requests aggregation.
	// No separate daily_totals table exists in the current schema (001_init.sql).
	// In production, create a daily_totals table and do an UPSERT here:
	//
	//   INSERT INTO daily_totals (wallet, date, total_minor)
	//   VALUES ($1, $2::date, $3)
	//   ON CONFLICT (wallet, date) DO UPDATE SET total_minor = daily_totals.total_minor + $3
	//
	// For now, return nil — the limits are enforced via CheckDailyLimit which
	// aggregates mint_requests directly.
	return nil
}

// InsertAuditLog writes an audit event inside an existing transaction.
// If the audit repository is not yet wired (nil), this method allows the caller
// to still record events directly.
func (r *MintRepository) InsertAuditLog(ctx context.Context, tx *sql.Tx, event *model.AuditEvent) error {
	stmt := `INSERT INTO audit_log
		(event_id, event_type, actor_type, actor_id, resource_type, resource_id, action, details, ip_address, user_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := tx.ExecContext(ctx, stmt,
		event.EventID, event.EventType, event.ActorType, event.ActorID,
		event.ResourceType, event.ResourceID, event.Action,
		event.Details, event.IPAddress, event.UserAgent,
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert audit log %s: %w", event.EventID, err)
	}
	return nil
}

// GenerateID creates a random hex-encoded ID with the given prefix.
// Uses crypto/rand for unpredictability (important for idempotency and audit).
func GenerateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
