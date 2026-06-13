package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/model"
)

// ChainRepository handles persistence for on-chain transactions and reserve accounts.
type ChainRepository struct {
	db *sql.DB
}

// NewChainRepository creates a new ChainRepository backed by the given *sql.DB.
func NewChainRepository(db *sql.DB) *ChainRepository {
	return &ChainRepository{db: db}
}

// SaveChainTxWithTx inserts a new chain transaction record within an existing transaction.
// Use this when the outbox insert must be in the same unit of work.
func (r *ChainRepository) SaveChainTxWithTx(ctx context.Context, tx *sql.Tx, chainTx *model.ChainTx) error {
	if chainTx.TxHash == "" || chainTx.Network == "" {
		return fmt.Errorf("chain_repository: tx_hash and network are required")
	}
	if !model.ValidNetwork(chainTx.Network) {
		return fmt.Errorf("chain_repository: unknown network %q", chainTx.Network)
	}

	stmt := `INSERT INTO chain_txs (network, tx_hash, tx_type, status, confirmations, raw_json)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := tx.ExecContext(ctx, stmt,
		chainTx.Network, chainTx.TxHash, chainTx.TxType, chainTx.Status,
		chainTx.Confirmations, chainTx.RawJSON,
	)
	if err != nil {
		return fmt.Errorf("chain_repository: save chain tx with tx %s/%s: %w", chainTx.Network, chainTx.TxHash, err)
	}
	return nil
}

// SaveChainTx inserts a new chain transaction record into the chain_txs table.
// If a row with the same (network, tx_hash) already exists the call is a no-op
// (checked via GetByTxHash before insert). Note: the chain_txs table does not
// have a UNIQUE constraint on (network, tx_hash) in the current schema; an
// application-level check prevents duplicates.
func (r *ChainRepository) SaveChainTx(ctx context.Context, tx *model.ChainTx) error {
	if tx.TxHash == "" || tx.Network == "" {
		return fmt.Errorf("chain_repository: tx_hash and network are required")
	}
	if !model.ValidNetwork(tx.Network) {
		return fmt.Errorf("chain_repository: unknown network %q", tx.Network)
	}

	// Check for existence first (no UNIQUE constraint on network+tx_hash).
	existing, err := r.GetByTxHash(ctx, tx.Network, tx.TxHash)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil // already exists, no-op
	}

	stmt := `INSERT INTO chain_txs (network, tx_hash, tx_type, status, confirmations, raw_json)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err = r.db.ExecContext(ctx, stmt,
		tx.Network, tx.TxHash, tx.TxType, tx.Status,
		tx.Confirmations, tx.RawJSON,
	)
	if err != nil {
		return fmt.Errorf("chain_repository: save chain tx %s/%s: %w", tx.Network, tx.TxHash, err)
	}
	return nil
}

// GetByTxHash retrieves a single chain transaction by network and tx_hash.
// Returns nil, nil when no matching row is found.
func (r *ChainRepository) GetByTxHash(ctx context.Context, network string, txHash string) (*model.ChainTx, error) {
	stmt := `SELECT id, network, tx_hash, tx_type, status, confirmations, raw_json, created_at, finalized_at
		FROM chain_txs WHERE network = $1 AND tx_hash = $2`

	var tx model.ChainTx
	var rawJSON sql.NullString
	err := r.db.QueryRowContext(ctx, stmt, network, txHash).Scan(
		&tx.ID, &tx.Network, &tx.TxHash, &tx.TxType, &tx.Status,
		&tx.Confirmations, &rawJSON, &tx.CreatedAt, &tx.FinalizedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chain_repository: get by tx_hash %s/%s: %w", network, txHash, err)
	}

	if rawJSON.Valid {
		tx.RawJSON = []byte(rawJSON.String)
	}
	return &tx, nil
}

// UpdateConfirmations updates the confirmation count and optionally the status
// of a chain transaction identified by its tx_hash.
func (r *ChainRepository) UpdateConfirmations(ctx context.Context, txHash string, confirmations int, status string) error {
	if status == "" {
		status = model.TxStatusConfirmed
	}
	stmt := `UPDATE chain_txs
		SET confirmations = $2, status = $3
		WHERE tx_hash = $1`

	_, err := r.db.ExecContext(ctx, stmt, txHash, confirmations, status)
	if err != nil {
		return fmt.Errorf("chain_repository: update confirmations %s: %w", txHash, err)
	}
	return nil
}

// MarkFinalized sets the status of a chain transaction to 'finalized' and records the time.
func (r *ChainRepository) MarkFinalized(ctx context.Context, txHash string) error {
	stmt := `UPDATE chain_txs
		SET status = $2, finalized_at = NOW()
		WHERE tx_hash = $1`

	_, err := r.db.ExecContext(ctx, stmt, txHash, model.TxStatusFinalized)
	if err != nil {
		return fmt.Errorf("chain_repository: mark finalized %s: %w", txHash, err)
	}
	return nil
}

// GetReserveAccount fetches the reserve account for a given network and asset symbol.
// Returns nil, nil when no matching account exists.
func (r *ChainRepository) GetReserveAccount(ctx context.Context, network string, assetSymbol string) (*model.ReserveAccount, error) {
	stmt := `SELECT id, network, asset_symbol, address, confirmed_balance_minor,
	                 pending_balance_minor, last_reconciled_at, created_at, updated_at
		FROM reserve_accounts WHERE network = $1 AND asset_symbol = $2`

	var acct model.ReserveAccount
	err := r.db.QueryRowContext(ctx, stmt, network, assetSymbol).Scan(
		&acct.ID, &acct.Network, &acct.AssetSymbol, &acct.Address,
		&acct.ConfirmedBalanceMinor, &acct.PendingBalanceMinor,
		&acct.LastReconciledAt, &acct.CreatedAt, &acct.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chain_repository: get reserve account %s/%s: %w", network, assetSymbol, err)
	}
	return &acct, nil
}

// ListReserveAccounts returns all reserve accounts, optionally filtered by network.
// Pass an empty network string to return all accounts.
func (r *ChainRepository) ListReserveAccounts(ctx context.Context, network string) ([]model.ReserveAccount, error) {
	var rows *sql.Rows
	var err error

	if network == "" {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, network, asset_symbol, address, confirmed_balance_minor,
			        pending_balance_minor, last_reconciled_at, created_at, updated_at
			 FROM reserve_accounts ORDER BY network, asset_symbol`)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, network, asset_symbol, address, confirmed_balance_minor,
			        pending_balance_minor, last_reconciled_at, created_at, updated_at
			 FROM reserve_accounts WHERE network = $1 ORDER BY asset_symbol`, network)
	}
	if err != nil {
		return nil, fmt.Errorf("chain_repository: list reserve accounts: %w", err)
	}
	defer rows.Close()

	var accounts []model.ReserveAccount
	for rows.Next() {
		var a model.ReserveAccount
		if err := rows.Scan(
			&a.ID, &a.Network, &a.AssetSymbol, &a.Address,
			&a.ConfirmedBalanceMinor, &a.PendingBalanceMinor,
			&a.LastReconciledAt, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("chain_repository: scan reserve account: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chain_repository: iterate reserve accounts: %w", err)
	}
	return accounts, nil
}

// generateID creates a random hex-encoded ID with the given prefix.
func generateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
