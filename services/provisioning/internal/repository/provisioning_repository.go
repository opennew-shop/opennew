// Package repository 封装服务开通对 order_intents、outbox 与 audit_log 表的数据访问，
// 包含订单意图的查询/行锁/状态流转、Outbox 事件的拉取与发布，以及审计日志写入。
package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ancf-commerce/ancf/services/provisioning/internal/model"
)

// ProvisioningRepository provides data-access methods for order_intents, outbox,
// and audit_log tables in the context of service provisioning.
type ProvisioningRepository struct {
	db *sql.DB
}

// NewProvisioningRepository creates a new ProvisioningRepository.
func NewProvisioningRepository(db *sql.DB) *ProvisioningRepository {
	return &ProvisioningRepository{db: db}
}

// GetOrderIntentByID retrieves an order intent row by its intent_id.
// Returns nil, nil when no matching row is found.
func (r *ProvisioningRepository) GetOrderIntentByID(ctx context.Context, intentID string) (*OrderIntentRow, error) {
	query := `SELECT intent_id, quote_id, wallet, network, currency, total_minor, status,
		signable_payload, created_at, updated_at
		FROM order_intents WHERE intent_id = $1`

	var row OrderIntentRow
	err := r.db.QueryRowContext(ctx, query, intentID).Scan(
		&row.IntentID, &row.QuoteID, &row.Wallet, &row.Network,
		&row.Currency, &row.TotalMinor, &row.Status,
		&row.SignablePayload, &row.CreatedAt, &row.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning_repository: get intent %s: %w", intentID, err)
	}
	return &row, nil
}

// UpdateOrderStatus updates the status of an order intent.
// Used to transition between committed -> provisioning -> completed/failed/refunded.
func (r *ProvisioningRepository) UpdateOrderStatus(ctx context.Context, intentID, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE order_intents SET status = $2, updated_at = NOW() WHERE intent_id = $1`,
		intentID, status)
	if err != nil {
		return fmt.Errorf("provisioning_repository: update status %s to %s: %w", intentID, status, err)
	}
	return nil
}

// UpdateOrderStatusWithTx updates the order intent status within an existing transaction.
func (r *ProvisioningRepository) UpdateOrderStatusWithTx(ctx context.Context, tx *sql.Tx, intentID, status string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE order_intents SET status = $2, updated_at = NOW() WHERE intent_id = $1`,
		intentID, status)
	if err != nil {
		return fmt.Errorf("provisioning_repository: update status tx %s to %s: %w", intentID, status, err)
	}
	return nil
}

// LockOrderIntentForUpdate acquires a row-level lock on the order intent within a transaction.
func (r *ProvisioningRepository) LockOrderIntentForUpdate(ctx context.Context, tx *sql.Tx, intentID string) (*OrderIntentRow, error) {
	query := `SELECT intent_id, quote_id, wallet, network, currency, total_minor, status,
		signable_payload, created_at, updated_at
		FROM order_intents WHERE intent_id = $1 FOR UPDATE`

	var row OrderIntentRow
	err := tx.QueryRowContext(ctx, query, intentID).Scan(
		&row.IntentID, &row.QuoteID, &row.Wallet, &row.Network,
		&row.Currency, &row.TotalMinor, &row.Status,
		&row.SignablePayload, &row.CreatedAt, &row.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning_repository: lock for update %s: %w", intentID, err)
	}
	return &row, nil
}

// FetchPendingOutboxEvents retrieves pending outbox events for the provisioning service.
// It filters by event_type = 'order_committed' and status = 'pending'.
// Uses FOR UPDATE SKIP LOCKED for safe concurrent processing across multiple instances.
func (r *ProvisioningRepository) FetchPendingOutboxEvents(ctx context.Context, limit int) ([]model.OutboxEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, event_id, event_type, aggregate_type, aggregate_id, payload, status, created_at
		 FROM outbox
		 WHERE event_type = $1 AND status = 'pending'
		 ORDER BY created_at ASC LIMIT $2
		 FOR UPDATE SKIP LOCKED`,
		model.EventTypeOrderCommitted, limit)
	if err != nil {
		return nil, fmt.Errorf("provisioning_repository: fetch pending events: %w", err)
	}
	defer rows.Close()

	var events []model.OutboxEvent
	for rows.Next() {
		var evt model.OutboxEvent
		if err := rows.Scan(
			&evt.ID, &evt.EventID, &evt.EventType, &evt.AggregateType,
			&evt.AggregateID, &evt.Payload, &evt.Status, &evt.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("provisioning_repository: scan event: %w", err)
		}
		events = append(events, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("provisioning_repository: rows iteration: %w", err)
	}
	return events, nil
}

// MarkOutboxPublished marks an outbox event as published within a transaction.
// The provisioning service "consumes" the order_committed event by marking it published.
func (r *ProvisioningRepository) MarkOutboxPublished(ctx context.Context, tx *sql.Tx, eventID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE outbox SET status = 'published', processed_at = NOW() WHERE event_id = $1`,
		eventID)
	if err != nil {
		return fmt.Errorf("provisioning_repository: mark published %s: %w", eventID, err)
	}
	return nil
}

// InsertOutboxEventWithTx inserts a new outbox event within a transaction.
// Used to emit provisioning_started, provisioning_completed, or provisioning_failed events.
func (r *ProvisioningRepository) InsertOutboxEventWithTx(ctx context.Context, tx *sql.Tx, eventID, eventType, aggregateType, aggregateID string, payload json.RawMessage) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (event_id, event_type, aggregate_type, aggregate_id, payload, status)
		 VALUES ($1, $2, $3, $4, $5, 'pending')`,
		eventID, eventType, aggregateType, aggregateID, payload)
	if err != nil {
		return fmt.Errorf("provisioning_repository: insert outbox event %s: %w", eventID, err)
	}
	return nil
}

// InsertAuditEventWithTx inserts an immutable audit log entry within a transaction.
func (r *ProvisioningRepository) InsertAuditEventWithTx(ctx context.Context, tx *sql.Tx, evt *model.AuditEvent) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log (event_id, event_type, actor_type, actor_id, resource_type, resource_id, action, details)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		evt.EventID, evt.EventType, evt.ActorType, evt.ActorID,
		evt.ResourceType, evt.ResourceID, evt.Action, evt.Details)
	if err != nil {
		return fmt.Errorf("provisioning_repository: insert audit event %s: %w", evt.EventID, err)
	}
	return nil
}

// GetProvisioningResultByIntentID retrieves a stored provisioning result from the
// audit_log table (using event_type 'provisioning.completed') for a given order intent.
// This is used for the GET /api/v1/cli/provision-access/:intent_id endpoint.
func (r *ProvisioningRepository) GetProvisioningResultByIntentID(ctx context.Context, intentID string) (*model.ProvisioningResult, error) {
	query := `SELECT resource_id, details, created_at
		FROM audit_log
		WHERE resource_id = $1 AND event_type = $2
		ORDER BY created_at DESC LIMIT 1`

	var resourceID string
	var details []byte
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, query, intentID, model.AuditEventProvisioningCompleted).Scan(
		&resourceID, &details, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning_repository: get provisioning result %s: %w", intentID, err)
	}

	var result model.ProvisioningResult
	if err := json.Unmarshal(details, &result); err != nil {
		return nil, fmt.Errorf("provisioning_repository: unmarshal result %s: %w", intentID, err)
	}
	return &result, nil
}

// OrderIntentRow is a lightweight representation of an order_intents row
// used by the provisioning service. It contains only the fields needed for provisioning.
type OrderIntentRow struct {
	IntentID        string          `json:"intent_id"`
	QuoteID         string          `json:"quote_id"`
	Wallet          string          `json:"wallet"`
	Network         string          `json:"network"`
	Currency        string          `json:"currency"`
	TotalMinor      int64           `json:"total_minor"`
	Status          string          `json:"status"`
	SignablePayload json.RawMessage `json:"signable_payload"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}
