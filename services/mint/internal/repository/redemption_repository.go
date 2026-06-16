package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/ancf-commerce/ancf/services/mint/internal/model"
)

// RedemptionRepository handles persistence for redemption_requests and reserve_account queries.
// 赎回数据访问层：负责 redemption_requests 表持久化与 reserve_account 储备查询。
type RedemptionRepository struct {
	db *sql.DB
}

// NewRedemptionRepository creates a new RedemptionRepository backed by the given *sql.DB.
func NewRedemptionRepository(db *sql.DB) *RedemptionRepository {
	return &RedemptionRepository{db: db}
}

// CreateRedemptionRequest inserts a new redemption request into the database.
// Must be called within a transaction (tx).
func (r *RedemptionRepository) CreateRedemptionRequest(ctx context.Context, tx *sql.Tx, rec *model.RedemptionRecord) error {
	if rec.RequestID == "" {
		rec.RequestID = generateID("red_")
	}

	stmt := `INSERT INTO redemption_requests
		(request_id, wallet, asset_id, amount_minor, status, burn_tx_id, payout_tx_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := tx.ExecContext(ctx, stmt,
		rec.RequestID,
		rec.Wallet,
		rec.AssetID,
		rec.AmountMinor,
		rec.Status,
		rec.BurnTxID,
		rec.PayoutTxID,
		rec.CreatedAt,
		rec.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("redemption_repository: create request %s: %w", rec.RequestID, err)
	}
	return nil
}

// GetByRequestID retrieves a redemption request by its request_id.
// Returns nil, nil when no matching request is found.
func (r *RedemptionRepository) GetByRequestID(ctx context.Context, requestID string) (*model.RedemptionRecord, error) {
	stmt := `SELECT
		id, request_id, wallet, asset_id, amount_minor, status,
		burn_tx_id, payout_tx_id, created_at, updated_at
	FROM redemption_requests WHERE request_id = $1`

	var rec model.RedemptionRecord
	err := r.db.QueryRowContext(ctx, stmt, requestID).Scan(
		&rec.ID, &rec.RequestID, &rec.Wallet, &rec.AssetID,
		&rec.AmountMinor, &rec.Status, &rec.BurnTxID, &rec.PayoutTxID,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redemption_repository: get by request_id %s: %w", requestID, err)
	}
	return &rec, nil
}

// UpdateStatus updates the status of a redemption request within a transaction.
func (r *RedemptionRepository) UpdateStatus(ctx context.Context, tx *sql.Tx, requestID string, status string) error {
	stmt := `UPDATE redemption_requests
		SET status = $2, updated_at = NOW()
		WHERE request_id = $1`

	result, err := tx.ExecContext(ctx, stmt, requestID, status)
	if err != nil {
		return fmt.Errorf("redemption_repository: update status %s to %s: %w", requestID, status, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("redemption_repository: request %s not found for status update", requestID)
	}
	return nil
}

// UpdatePayoutTxID sets the payout_tx_id on a redemption request within a transaction.
func (r *RedemptionRepository) UpdatePayoutTxID(ctx context.Context, tx *sql.Tx, requestID string, payoutTxID string) error {
	stmt := `UPDATE redemption_requests
		SET payout_tx_id = $2, updated_at = NOW()
		WHERE request_id = $1`

	result, err := tx.ExecContext(ctx, stmt, requestID, payoutTxID)
	if err != nil {
		return fmt.Errorf("redemption_repository: update payout_tx_id %s: %w", requestID, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("redemption_repository: request %s not found for payout_tx_id update", requestID)
	}
	return nil
}

// LockForUpdate locks a redemption request row for update within a transaction (SELECT FOR UPDATE).
// Returns the locked record, or nil, nil if not found.
func (r *RedemptionRepository) LockForUpdate(ctx context.Context, tx *sql.Tx, requestID string) (*model.RedemptionRecord, error) {
	stmt := `SELECT
		id, request_id, wallet, asset_id, amount_minor, status,
		burn_tx_id, payout_tx_id, created_at, updated_at
	FROM redemption_requests WHERE request_id = $1 FOR UPDATE`

	var rec model.RedemptionRecord
	err := tx.QueryRowContext(ctx, stmt, requestID).Scan(
		&rec.ID, &rec.RequestID, &rec.Wallet, &rec.AssetID,
		&rec.AmountMinor, &rec.Status, &rec.BurnTxID, &rec.PayoutTxID,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redemption_repository: lock for update %s: %w", requestID, err)
	}
	return &rec, nil
}

// GetReserveAccount retrieves the reserve account for a given network and asset symbol.
func (r *RedemptionRepository) GetReserveAccount(ctx context.Context, network, assetSymbol string) (*model.ReserveAccount, error) {
	stmt := `SELECT id, network, asset_symbol, address
	FROM reserve_accounts
	WHERE network = $1 AND asset_symbol = $2`

	var ra model.ReserveAccount
	err := r.db.QueryRowContext(ctx, stmt, network, assetSymbol).Scan(
		&ra.ID, &ra.Network, &ra.AssetSymbol, &ra.Address,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("redemption_repository: no reserve account for network=%s asset=%s", network, assetSymbol)
	}
	if err != nil {
		return nil, fmt.Errorf("redemption_repository: get reserve account: %w", err)
	}
	return &ra, nil
}

// GetAssetID retrieves the asset ID for a given symbol and network.
func (r *RedemptionRepository) GetAssetID(ctx context.Context, symbol, network string) (int64, error) {
	stmt := `SELECT id FROM assets WHERE symbol = $1 AND network = $2 AND status = 'active'`

	var id int64
	err := r.db.QueryRowContext(ctx, stmt, symbol, network).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("redemption_repository: no active asset for symbol=%s network=%s", symbol, network)
	}
	if err != nil {
		return 0, fmt.Errorf("redemption_repository: get asset id: %w", err)
	}
	return id, nil
}

// InsertAuditLog inserts an immutable audit event record within a transaction.
// Uses the audit_log table with event_type = 'redemption'.
func (r *RedemptionRepository) InsertAuditLog(ctx context.Context, tx *sql.Tx, eventID, eventType, action, resourceID, actorID string, details string) error {
	stmt := `INSERT INTO audit_log
		(event_id, event_type, actor_type, actor_id, resource_type, resource_id, action, details)
		VALUES ($1, $2, 'system', $3, 'redemption_request', $4, $5, $6)`

	_, err := tx.ExecContext(ctx, stmt, eventID, eventType, actorID, resourceID, action, details)
	if err != nil {
		return fmt.Errorf("redemption_repository: insert audit log: %w", err)
	}
	return nil
}

// generateID creates a random hex-encoded ID with the given prefix.
// Uses crypto/rand for unpredictability (important for idempotency and audit).
func generateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
