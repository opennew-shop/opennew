// Package repository 提供双分录账本的持久化访问。
// 所有写入方法要求调用方传入 *sql.Tx 以掌控事务边界;
// 余额读取可经 pg_advisory_xact_lock 串行化同一钱包的并发访问。
package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/ancf-commerce/ancf/services/ledger/internal/model"
)

// LedgerRepository handles all ledger persistence operations.
// All mutation methods require an explicit *sql.Tx to ensure callers
// control the transaction boundary (see demo.md Section 11).
type LedgerRepository struct {
	db *sql.DB
}

// NewLedgerRepository creates a new LedgerRepository backed by the given *sql.DB.
func NewLedgerRepository(db *sql.DB) *LedgerRepository {
	return &LedgerRepository{db: db}
}

// PostTransaction inserts one or more ledger entries inside an existing transaction.
// Each entry is self-balancing (one debit account, one credit account, same amount).
// Caller owns the transaction lifecycle (BEGIN/COMMIT/ROLLBACK).
func (r *LedgerRepository) PostTransaction(ctx context.Context, tx *sql.Tx, entries []model.LedgerEntry) error {
	stmt := `INSERT INTO ledger_entries
		(entry_id, transaction_id, wallet, debit_account, credit_account, amount_minor, currency, entry_type, reference_id, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	for i := range entries {
		e := &entries[i]
		if e.EntryID == "" {
			e.EntryID = generateID("entry_")
		}
		if e.TransactionID == "" {
			return fmt.Errorf("post entry %d: transaction_id is required", i)
		}
		if e.AmountMinor <= 0 {
			return fmt.Errorf("post entry %s: amount_minor must be > 0, got %d", e.EntryID, e.AmountMinor)
		}

		_, err := tx.ExecContext(ctx, stmt,
			e.EntryID,
			e.TransactionID,
			e.Wallet,
			e.DebitAccount,
			e.CreditAccount,
			e.AmountMinor,
			e.Currency,
			e.EntryType,
			e.ReferenceID,
			e.Metadata,
		)
		if err != nil {
			return fmt.Errorf("post entry %s: %w", e.EntryID, err)
		}
	}
	return nil
}

// GetBalance computes the aggregate wallet balance by summing ledger_entries.
//
// Calculation (all within wallet + currency scope):
//
//	available = mint_credit→user_available + purchase_refund→user_available
//	          - purchase_hold→user_available - redemption_debit→user_available
//
//	pending   = purchase_hold→user_pending
//	          - purchase_settle→user_pending - purchase_refund→user_pending
//
//	total_debit  = sum of all debits against user_available
//	total_credit = sum of all credits against user_available
//
// Note: this is a derived materialized view. Production should use a cached
// balance table updated atomically with ledger entry insertion.
func (r *LedgerRepository) GetBalance(ctx context.Context, wallet string, currency string) (*model.WalletBalance, error) {
	query := `
	SELECT
		COALESCE(SUM(CASE WHEN entry_type = 'mint_credit'     AND credit_account = 'user_available' THEN amount_minor ELSE 0 END), 0)
		- COALESCE(SUM(CASE WHEN entry_type = 'purchase_hold' AND debit_account  = 'user_available' THEN amount_minor ELSE 0 END), 0)
		- COALESCE(SUM(CASE WHEN entry_type = 'redemption_debit' AND debit_account = 'user_available' THEN amount_minor ELSE 0 END), 0)
		+ COALESCE(SUM(CASE WHEN entry_type = 'purchase_refund'  AND credit_account = 'user_available' THEN amount_minor ELSE 0 END), 0)
		AS available,

		COALESCE(SUM(CASE WHEN entry_type = 'purchase_hold'   AND credit_account = 'user_pending' THEN amount_minor ELSE 0 END), 0)
		- COALESCE(SUM(CASE WHEN entry_type IN ('purchase_settle', 'purchase_refund') AND debit_account = 'user_pending' THEN amount_minor ELSE 0 END), 0)
		AS pending,

		COALESCE(SUM(CASE WHEN debit_account  = 'user_available' THEN amount_minor ELSE 0 END), 0) AS total_debit,
		COALESCE(SUM(CASE WHEN credit_account = 'user_available' THEN amount_minor ELSE 0 END), 0) AS total_credit
	FROM ledger_entries
	WHERE wallet = $1 AND currency = $2`

	b := &model.WalletBalance{
		Wallet:   wallet,
		Currency: currency,
	}
	err := r.db.QueryRowContext(ctx, query, wallet, currency).Scan(
		&b.Available, &b.Pending, &b.TotalDebit, &b.TotalCredit,
	)
	if err != nil {
		return nil, fmt.Errorf("get balance for wallet %s: %w", wallet, err)
	}
	return b, nil
}

// GetBalanceForUpdate computes the aggregate balance within a transaction,
// acquiring a PostgreSQL advisory lock to prevent concurrent modifications.
// Use this inside checkout-commit transactions to ensure consistent reads.
//
// pg_advisory_xact_lock is transaction-scoped; it is released automatically
// on COMMIT or ROLLBACK, so no explicit unlock is needed.
func (r *LedgerRepository) GetBalanceForUpdate(ctx context.Context, tx *sql.Tx, wallet string, currency string) (*model.WalletBalance, error) {
	// Acquire a transaction-scoped advisory lock on the wallet to serialise
	// concurrent balance reads/writes for the same wallet.
	lockKey := "wallet_balance:" + wallet + ":" + currency
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey)
	if err != nil {
		return nil, fmt.Errorf("acquire balance lock for wallet %s: %w", wallet, err)
	}

	query := `
	SELECT
		COALESCE(SUM(CASE WHEN entry_type = 'mint_credit'     AND credit_account = 'user_available' THEN amount_minor ELSE 0 END), 0)
		- COALESCE(SUM(CASE WHEN entry_type = 'purchase_hold' AND debit_account  = 'user_available' THEN amount_minor ELSE 0 END), 0)
		- COALESCE(SUM(CASE WHEN entry_type = 'redemption_debit' AND debit_account = 'user_available' THEN amount_minor ELSE 0 END), 0)
		+ COALESCE(SUM(CASE WHEN entry_type = 'purchase_refund'  AND credit_account = 'user_available' THEN amount_minor ELSE 0 END), 0)
		AS available,

		COALESCE(SUM(CASE WHEN entry_type = 'purchase_hold'   AND credit_account = 'user_pending' THEN amount_minor ELSE 0 END), 0)
		- COALESCE(SUM(CASE WHEN entry_type IN ('purchase_settle', 'purchase_refund') AND debit_account = 'user_pending' THEN amount_minor ELSE 0 END), 0)
		AS pending,

		COALESCE(SUM(CASE WHEN debit_account  = 'user_available' THEN amount_minor ELSE 0 END), 0) AS total_debit,
		COALESCE(SUM(CASE WHEN credit_account = 'user_available' THEN amount_minor ELSE 0 END), 0) AS total_credit
	FROM ledger_entries
	WHERE wallet = $1 AND currency = $2`

	b := &model.WalletBalance{
		Wallet:   wallet,
		Currency: currency,
	}
	err = tx.QueryRowContext(ctx, query, wallet, currency).Scan(
		&b.Available, &b.Pending, &b.TotalDebit, &b.TotalCredit,
	)
	if err != nil {
		return nil, fmt.Errorf("get balance for update wallet %s: %w", wallet, err)
	}
	return b, nil
}

// GetEntries returns a paginated list of ledger entries for a wallet,
// ordered by creation time descending (most recent first).
func (r *LedgerRepository) GetEntries(ctx context.Context, wallet string, limit, offset int) ([]model.LedgerEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT entry_id, transaction_id, wallet, debit_account, credit_account,
		        amount_minor, currency, entry_type, reference_id, metadata, created_at
		 FROM ledger_entries
		 WHERE wallet = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		wallet, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("get entries for wallet %s: %w", wallet, err)
	}
	defer rows.Close()

	var entries []model.LedgerEntry
	for rows.Next() {
		var e model.LedgerEntry
		if err := rows.Scan(
			&e.EntryID, &e.TransactionID, &e.Wallet,
			&e.DebitAccount, &e.CreditAccount, &e.AmountMinor,
			&e.Currency, &e.EntryType, &e.ReferenceID,
			&e.Metadata, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan entry for wallet %s: %w", wallet, err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entries for wallet %s: %w", wallet, err)
	}
	return entries, nil
}

// generateID creates a random hex-encoded ID with the given prefix.
// Uses crypto/rand for unpredictability (important for idempotency and audit).
func generateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
