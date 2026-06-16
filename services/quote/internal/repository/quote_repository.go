// Package repository 提供报价表(quotes)的数据访问。
// 通过 SELECT FOR UPDATE 行锁与 consumed=FALSE 条件更新,防止报价被并发重复消费。
package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ancf-commerce/ancf/services/quote/internal/model"
)

// QuoteRepository provides data-access methods for the quotes table.
type QuoteRepository struct {
	db *sql.DB
}

// NewQuoteRepository creates a new QuoteRepository with the given database connection.
func NewQuoteRepository(db *sql.DB) *QuoteRepository {
	return &QuoteRepository{db: db}
}

// Create inserts a new quote record into the quotes table.
func (r *QuoteRepository) Create(ctx context.Context, q *model.Quote) error {
	sqlStmt := `INSERT INTO quotes (
		quote_id, wallet, network, currency, total_minor, scale,
		expires_at, consumed, consumed_at, lines, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := r.db.ExecContext(ctx, sqlStmt,
		q.QuoteID, q.Wallet, q.Network, q.Currency,
		q.TotalMinor, q.Scale,
		q.ExpiresAt, q.Consumed, q.ConsumedAt,
		q.Lines, q.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("quote_repository: create quote %s: %w", q.QuoteID, err)
	}
	return nil
}

// GetByQuoteID retrieves a single quote by its quote_id.
// Returns nil, nil when no matching quote is found.
func (r *QuoteRepository) GetByQuoteID(ctx context.Context, quoteID string) (*model.Quote, error) {
	sqlStmt := `SELECT
		id, quote_id, wallet, network, currency, total_minor, scale,
		expires_at, consumed, consumed_at, lines, created_at
	FROM quotes WHERE quote_id = $1`

	var q model.Quote
	err := r.db.QueryRowContext(ctx, sqlStmt, quoteID).Scan(
		&q.ID, &q.QuoteID, &q.Wallet, &q.Network, &q.Currency,
		&q.TotalMinor, &q.Scale,
		&q.ExpiresAt, &q.Consumed, &q.ConsumedAt,
		&q.Lines, &q.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("quote_repository: get by quote_id %s: %w", quoteID, err)
	}
	return &q, nil
}

// MarkConsumed atomically marks a quote as consumed.
// Uses UPDATE ... WHERE consumed = FALSE to prevent double-consumption.
// Returns true if the quote was successfully marked consumed (affected rows > 0).
// Returns false if the quote was already consumed or does not exist.
func (r *QuoteRepository) MarkConsumed(ctx context.Context, quoteID string) (bool, error) {
	sqlStmt := `UPDATE quotes SET consumed = TRUE, consumed_at = NOW()
	WHERE quote_id = $1 AND consumed = FALSE`

	result, err := r.db.ExecContext(ctx, sqlStmt, quoteID)
	if err != nil {
		return false, fmt.Errorf("quote_repository: mark consumed %s: %w", quoteID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("quote_repository: rows affected for %s: %w", quoteID, err)
	}

	return affected > 0, nil
}

// IsExpired checks whether a quote has passed its expiration time.
// Returns true if the quote exists and its expires_at is in the past.
// Returns false if the quote still has remaining validity or does not exist.
func (r *QuoteRepository) IsExpired(ctx context.Context, quoteID string) (bool, error) {
	sqlStmt := `SELECT COUNT(*) FROM quotes
	WHERE quote_id = $1 AND expires_at <= NOW()`

	var count int
	if err := r.db.QueryRowContext(ctx, sqlStmt, quoteID).Scan(&count); err != nil {
		return false, fmt.Errorf("quote_repository: is expired %s: %w", quoteID, err)
	}

	return count > 0, nil
}

// MarkConsumedWithTx atomically marks a quote as consumed within a transaction.
// Uses UPDATE ... WHERE consumed = FALSE in a pessimistic-locked context.
// The caller must have already locked the quote row via LockQuoteForUpdate.
// Returns true if the quote was successfully marked consumed (affected rows > 0).
// Returns false if the quote was already consumed or does not exist.
func (r *QuoteRepository) MarkConsumedWithTx(ctx context.Context, tx *sql.Tx, quoteID string) (bool, error) {
	sqlStmt := `UPDATE quotes SET consumed = TRUE, consumed_at = NOW()
		WHERE quote_id = $1 AND consumed = FALSE`

	result, err := tx.ExecContext(ctx, sqlStmt, quoteID)
	if err != nil {
		return false, fmt.Errorf("quote_repository: mark consumed tx %s: %w", quoteID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("quote_repository: rows affected tx for %s: %w", quoteID, err)
	}

	return affected > 0, nil
}

// LockQuoteForUpdate locks a quote row for update within a transaction (SELECT FOR UPDATE).
// This prevents concurrent consumption of the same quote.
// Returns the quote if found, or nil, nil if not found.
func (r *QuoteRepository) LockQuoteForUpdate(ctx context.Context, tx *sql.Tx, quoteID string) (*model.Quote, error) {
	sqlStmt := `SELECT
		id, quote_id, wallet, network, currency, total_minor, scale,
		expires_at, consumed, consumed_at, lines, created_at
	FROM quotes WHERE quote_id = $1 FOR UPDATE`

	var q model.Quote
	err := tx.QueryRowContext(ctx, sqlStmt, quoteID).Scan(
		&q.ID, &q.QuoteID, &q.Wallet, &q.Network, &q.Currency,
		&q.TotalMinor, &q.Scale,
		&q.ExpiresAt, &q.Consumed, &q.ConsumedAt,
		&q.Lines, &q.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("quote_repository: lock quote for update %s: %w", quoteID, err)
	}
	return &q, nil
}
