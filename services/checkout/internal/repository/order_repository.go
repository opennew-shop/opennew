package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ancf-commerce/ancf/services/checkout/internal/model"
)

// compile-time check: OrderRepository implements the OrderRepository interface.
var _ OrderRepo = (*OrderRepository)(nil)

// OrderRepository provides data-access methods for order_intents and idempotency_keys tables.
type OrderRepository struct {
	db *sql.DB
}

// NewOrderRepository creates a new OrderRepository with the given database connection.
func NewOrderRepository(db *sql.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

// CreateIntent inserts a new order intent record into the order_intents table.
func (r *OrderRepository) CreateIntent(ctx context.Context, intent *model.OrderIntent) error {
	sqlStmt := `INSERT INTO order_intents (
		intent_id, quote_id, wallet, network, currency, total_minor, status,
		idempotency_key, wallet_signature, agent_session_id, nonce,
		signable_payload, created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`

	_, err := r.db.ExecContext(ctx, sqlStmt,
		intent.IntentID, intent.QuoteID, intent.Wallet, intent.Network,
		intent.Currency, intent.TotalMinor, intent.Status,
		intent.IdempotencyKey, intent.WalletSignature, intent.AgentSessionID,
		intent.Nonce, intent.SignablePayload, intent.CreatedAt, intent.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("order_repository: create intent %s: %w", intent.IntentID, err)
	}
	return nil
}

// SECURITY FIX: F-001-01 — Look up existing order intent by idempotency key for prepare replay.
// GetByIdempotencyKey retrieves an order intent by its idempotency_key.
// Returns nil, nil when no matching intent is found (key not yet used).
func (r *OrderRepository) GetByIdempotencyKey(ctx context.Context, key string) (*model.OrderIntent, error) {
	sqlStmt := `SELECT
		id, intent_id, quote_id, wallet, network, currency, total_minor, status,
		idempotency_key, wallet_signature, agent_session_id, nonce,
		signable_payload, created_at, updated_at
	FROM order_intents WHERE idempotency_key = $1
	ORDER BY created_at DESC LIMIT 1`

	var intent model.OrderIntent
	var id int64
	err := r.db.QueryRowContext(ctx, sqlStmt, key).Scan(
		&id, &intent.IntentID, &intent.QuoteID, &intent.Wallet, &intent.Network,
		&intent.Currency, &intent.TotalMinor, &intent.Status,
		&intent.IdempotencyKey, &intent.WalletSignature, &intent.AgentSessionID,
		&intent.Nonce, &intent.SignablePayload, &intent.CreatedAt, &intent.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("order_repository: get by idempotency_key: %w", err)
	}
	return &intent, nil
}

// GetByIntentID retrieves a single order intent by its intent_id.
// Returns nil, nil when no matching intent is found.
func (r *OrderRepository) GetByIntentID(ctx context.Context, intentID string) (*model.OrderIntent, error) {
	sqlStmt := `SELECT
		id, intent_id, quote_id, wallet, network, currency, total_minor, status,
		idempotency_key, wallet_signature, agent_session_id, nonce,
		signable_payload, created_at, updated_at
	FROM order_intents WHERE intent_id = $1`

	var intent model.OrderIntent
	var id int64
	err := r.db.QueryRowContext(ctx, sqlStmt, intentID).Scan(
		&id, &intent.IntentID, &intent.QuoteID, &intent.Wallet, &intent.Network,
		&intent.Currency, &intent.TotalMinor, &intent.Status,
		&intent.IdempotencyKey, &intent.WalletSignature, &intent.AgentSessionID,
		&intent.Nonce, &intent.SignablePayload, &intent.CreatedAt, &intent.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("order_repository: get by intent_id %s: %w", intentID, err)
	}
	return &intent, nil
}

// UpdateStatus updates the status and wallet_signature of an order intent.
func (r *OrderRepository) UpdateStatus(ctx context.Context, intentID, status string, walletSignature *string) error {
	sqlStmt := `UPDATE order_intents
	SET status = $2, wallet_signature = $3, updated_at = NOW()
	WHERE intent_id = $1`

	_, err := r.db.ExecContext(ctx, sqlStmt, intentID, status, walletSignature)
	if err != nil {
		return fmt.Errorf("order_repository: update status %s: %w", intentID, err)
	}
	return nil
}

// CheckIdempotencyKey looks up an idempotency key in the registry.
//
// Returns a CachedResponse if the key exists and has not expired.
// If the key exists but the request body hash does NOT match, returns a 409 Conflict error.
// If the key does not exist, returns nil (the caller should proceed).
func (r *OrderRepository) CheckIdempotencyKey(ctx context.Context, key, bodyHash string) (*model.CachedResponse, error) {
	sqlStmt := `SELECT status_code, response_body, request_body_hash
	FROM idempotency_keys WHERE key = $1 AND expires_at > NOW()`

	var statusCode int
	var responseBody string
	var storedHash string
	err := r.db.QueryRowContext(ctx, sqlStmt, key).Scan(&statusCode, &responseBody, &storedHash)
	if err == sql.ErrNoRows {
		// Key does not exist or has expired; caller should proceed.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("order_repository: check idempotency key: %w", err)
	}

	// Key exists. Verify that the request body hash matches.
	if storedHash != bodyHash {
		return nil, fmt.Errorf("idempotency key %s was used with a different request body", key)
	}

	// Return the cached response for idempotent replay.
	return &model.CachedResponse{
		StatusCode:   statusCode,
		ResponseBody: responseBody,
	}, nil
}

// SaveIdempotencyKey records a new idempotency key with its response.
// The key expires after 24 hours (database default).
func (r *OrderRepository) SaveIdempotencyKey(ctx context.Context, key, bodyHash string, statusCode int, responseBody string) error {
	sqlStmt := `INSERT INTO idempotency_keys (key, request_body_hash, response_body, status_code)
	VALUES ($1, $2, $3, $4)`

	_, err := r.db.ExecContext(ctx, sqlStmt, key, bodyHash, responseBody, statusCode)
	if err != nil {
		return fmt.Errorf("order_repository: save idempotency key: %w", err)
	}
	return nil
}

// UpdateStatusWithTx updates the status of an order intent within a transaction.
// This ensures the status change is atomic with other transactional operations (quote consumption, etc.).
func (r *OrderRepository) UpdateStatusWithTx(ctx context.Context, tx *sql.Tx, intentID, status string) error {
	sqlStmt := `UPDATE order_intents
		SET status = $2, updated_at = NOW()
		WHERE intent_id = $1`

	_, err := tx.ExecContext(ctx, sqlStmt, intentID, status)
	if err != nil {
		return fmt.Errorf("order_repository: update status tx %s: %w", intentID, err)
	}
	return nil
}

// LockIntentForUpdate locks an order intent row for update within a transaction (SELECT FOR UPDATE).
// This prevents concurrent modifications to the same intent.
// Returns the intent if found, or nil, nil if not found.
func (r *OrderRepository) LockIntentForUpdate(ctx context.Context, tx *sql.Tx, intentID string) (*model.OrderIntent, error) {
	sqlStmt := `SELECT
		id, intent_id, quote_id, wallet, network, currency, total_minor, status,
		idempotency_key, wallet_signature, agent_session_id, nonce,
		signable_payload, created_at, updated_at
	FROM order_intents WHERE intent_id = $1 FOR UPDATE`

	var intent model.OrderIntent
	var id int64
	err := tx.QueryRowContext(ctx, sqlStmt, intentID).Scan(
		&id, &intent.IntentID, &intent.QuoteID, &intent.Wallet, &intent.Network,
		&intent.Currency, &intent.TotalMinor, &intent.Status,
		&intent.IdempotencyKey, &intent.WalletSignature, &intent.AgentSessionID,
		&intent.Nonce, &intent.SignablePayload, &intent.CreatedAt, &intent.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("order_repository: lock intent for update %s: %w", intentID, err)
	}
	return &intent, nil
}

// SaveIdempotencyResponseTx saves an idempotency key and its response within a transaction.
// This ensures the idempotency key is saved atomically with the business operations.
// The key expires after 24 hours (database default on expires_at).
func (r *OrderRepository) SaveIdempotencyResponseTx(ctx context.Context, tx *sql.Tx, key, bodyHash string, statusCode int, responseBody string) error {
	sqlStmt := `INSERT INTO idempotency_keys (key, request_body_hash, response_body, status_code)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE
		SET status_code = EXCLUDED.status_code, response_body = EXCLUDED.response_body`

	_, err := tx.ExecContext(ctx, sqlStmt, key, bodyHash, responseBody, statusCode)
	if err != nil {
		return fmt.Errorf("order_repository: save idempotency response tx: %w", err)
	}
	return nil
}

// LockIdempotencyKeyTx attempts to insert an idempotency key as a transactional lock.
// If the key already exists (UNIQUE constraint), the method returns an error.
// This is used inside the commit transaction to prevent concurrent commits with the same key.
func (r *OrderRepository) LockIdempotencyKeyTx(ctx context.Context, tx *sql.Tx, key, bodyHash string) error {
	sqlStmt := `INSERT INTO idempotency_keys (key, request_body_hash, response_body, status_code)
		VALUES ($1, $2, '', 0)
		ON CONFLICT (key) DO NOTHING`

	result, err := tx.ExecContext(ctx, sqlStmt, key, bodyHash)
	if err != nil {
		return fmt.Errorf("order_repository: lock idempotency key tx: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("order_repository: rows affected for idempotency lock: %w", err)
	}

	if rowsAffected == 0 {
		// Key already exists. This is a conflict situation.
		return &IdempotencyConflictError{Key: key}
	}

	return nil
}

// LockAndSaveIdempotencyKeyTx atomically reserves an idempotency key and
// stores the response in a single INSERT. This eliminates the window between
// LockIdempotencyKeyTx and SaveIdempotencyResponseTx where a crash would leave
// the key locked with an empty response body.
//
// Returns IdempotencyConflictError when the key already exists (concurrent commit).
//
// SECURITY FIX: F-001-04 — Unified lock-and-save into a single atomic operation
// to prevent orphaned idempotency keys with empty responses.
func (r *OrderRepository) LockAndSaveIdempotencyKeyTx(ctx context.Context, tx *sql.Tx, key, bodyHash string, statusCode int, responseBody string) error {
	sqlStmt := `INSERT INTO idempotency_keys (key, request_body_hash, response_body, status_code)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO NOTHING`

	result, err := tx.ExecContext(ctx, sqlStmt, key, bodyHash, responseBody, statusCode)
	if err != nil {
		return fmt.Errorf("order_repository: lock and save idempotency key tx: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("order_repository: rows affected for idempotency lock+save: %w", err)
	}

	if rowsAffected == 0 {
		return &IdempotencyConflictError{Key: key}
	}

	return nil
}

// IdempotencyConflictError represents a conflict when a duplicate idempotency key is detected
// inside the commit transaction.
type IdempotencyConflictError struct {
	Key string
}

func (e *IdempotencyConflictError) Error() string {
	return fmt.Sprintf("idempotency key %s already exists (concurrent commit conflict)", e.Key)
}

// OrderRepo defines the interface for order repository operations.
// Used for compile-time interface compliance checking.
type OrderRepo interface {
	CreateIntent(ctx context.Context, intent *model.OrderIntent) error
	GetByIntentID(ctx context.Context, intentID string) (*model.OrderIntent, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*model.OrderIntent, error) // SECURITY FIX: F-001-01
	UpdateStatus(ctx context.Context, intentID, status string, walletSignature *string) error
	UpdateStatusWithTx(ctx context.Context, tx *sql.Tx, intentID, status string) error
	LockIntentForUpdate(ctx context.Context, tx *sql.Tx, intentID string) (*model.OrderIntent, error)
	CheckIdempotencyKey(ctx context.Context, key, bodyHash string) (*model.CachedResponse, error)
	SaveIdempotencyKey(ctx context.Context, key, bodyHash string, statusCode int, responseBody string) error
	SaveIdempotencyResponseTx(ctx context.Context, tx *sql.Tx, key, bodyHash string, statusCode int, responseBody string) error
	LockIdempotencyKeyTx(ctx context.Context, tx *sql.Tx, key, bodyHash string) error
	LockAndSaveIdempotencyKeyTx(ctx context.Context, tx *sql.Tx, key, bodyHash string, statusCode int, responseBody string) error
}
