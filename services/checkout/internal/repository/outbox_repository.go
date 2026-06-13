package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// OutboxEvent represents a single event in the outbox table, used for
// reliable event publishing via the outbox pattern.
//
// Events are inserted within the same database transaction as the business
// operation (checkout commit). A separate outbox processor polls for pending
// events and publishes them to Redis Streams.
type OutboxEvent struct {
	ID            int64           `json:"id"`
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	ProcessedAt   *time.Time      `json:"processed_at,omitempty"`
}

// OutboxRepository provides data-access methods for the outbox table.
type OutboxRepository struct {
	db *sql.DB
}

// NewOutboxRepository creates a new OutboxRepository with the given database connection.
func NewOutboxRepository(db *sql.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

// InsertWithTx inserts a new outbox event within a database transaction.
// The event is inserted with status 'pending'. It becomes visible to the outbox
// processor only after the transaction commits.
func (r *OutboxRepository) InsertWithTx(ctx context.Context, tx *sql.Tx, event *OutboxEvent) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (event_id, event_type, aggregate_type, aggregate_id, payload, status)
		 VALUES ($1, $2, $3, $4, $5, 'pending')`,
		event.EventID, event.EventType, event.AggregateType, event.AggregateID, event.Payload)
	if err != nil {
		return fmt.Errorf("outbox_repository: insert event %s: %w", event.EventID, err)
	}
	return nil
}

// FetchPending retrieves pending outbox events for processing, ordered by creation time.
// Uses FOR UPDATE SKIP LOCKED to allow multiple concurrent processor instances without blocking.
// limit controls the maximum number of events returned per batch.
func (r *OutboxRepository) FetchPending(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, event_id, event_type, aggregate_type, aggregate_id, payload, status, created_at
		 FROM outbox WHERE status = 'pending'
		 ORDER BY created_at ASC LIMIT $1
		 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox_repository: fetch pending: %w", err)
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var evt OutboxEvent
		if err := rows.Scan(
			&evt.ID, &evt.EventID, &evt.EventType, &evt.AggregateType,
			&evt.AggregateID, &evt.Payload, &evt.Status, &evt.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("outbox_repository: scan pending event: %w", err)
		}
		events = append(events, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox_repository: rows iteration: %w", err)
	}
	return events, nil
}

// MarkPublished marks an outbox event as successfully published.
// The update is performed within the provided transaction.
func (r *OutboxRepository) MarkPublished(ctx context.Context, tx *sql.Tx, eventID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE outbox SET status = 'published', processed_at = NOW()
		 WHERE event_id = $1`, eventID)
	if err != nil {
		return fmt.Errorf("outbox_repository: mark published %s: %w", eventID, err)
	}
	return nil
}

// MarkFailed marks an outbox event as failed after unsuccessful publishing attempts.
// The update is performed outside a transaction (auto-commit) to ensure the failure is recorded.
func (r *OutboxRepository) MarkFailed(ctx context.Context, eventID string, errMsg string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE outbox SET status = 'failed', processed_at = NOW()
		 WHERE event_id = $1`, eventID)
	if err != nil {
		return fmt.Errorf("outbox_repository: mark failed %s: %w", eventID, err)
	}
	return nil
}
