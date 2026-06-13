package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ancf-commerce/ancf/services/audit/internal/model"
)

// AuditRepository handles all persistence operations for the immutable audit_log table.
//
// All INSERTs MUST happen inside a caller-managed transaction to ensure
// atomicity with the business operation that triggered the audit event.
// Audit events are append-only — no UPDATE or DELETE is exposed.
type AuditRepository struct {
	db *sql.DB
}

// NewAuditRepository creates a new AuditRepository backed by the given *sql.DB.
func NewAuditRepository(db *sql.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Insert writes a single audit event inside an existing transaction.
// The caller is responsible for managing the transaction boundary.
//
// Validation performed:
//   - event_id must not be empty
//   - actor_type must be one of the allowed values
//   - event_type, resource_type, and action must not be empty
func (r *AuditRepository) Insert(ctx context.Context, tx *sql.Tx, event *model.AuditEvent) error {
	if event.EventID == "" {
		return fmt.Errorf("insert audit log: event_id is required")
	}
	if event.EventType == "" {
		return fmt.Errorf("insert audit log: event_type is required")
	}
	if event.ResourceType == "" {
		return fmt.Errorf("insert audit log: resource_type is required")
	}
	if event.Action == "" {
		return fmt.Errorf("insert audit log: action is required")
	}
	if err := model.ValidateActorType(event.ActorType); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}

	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	stmt := `INSERT INTO audit_log
		(event_id, event_type, actor_type, actor_id, resource_type, resource_id, action, details, ip_address, user_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := tx.ExecContext(ctx, stmt,
		event.EventID,
		event.EventType,
		event.ActorType,
		event.ActorID,
		event.ResourceType,
		event.ResourceID,
		event.Action,
		event.Details,
		event.IPAddress,
		event.UserAgent,
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert audit log %s: %w", event.EventID, err)
	}
	return nil
}

// InsertBatch writes multiple audit events inside an existing transaction.
// All events are inserted in a single round-trip using a multi-row INSERT.
// The caller is responsible for managing the transaction.
func (r *AuditRepository) InsertBatch(ctx context.Context, tx *sql.Tx, events []*model.AuditEvent) error {
	if len(events) == 0 {
		return nil
	}

	now := time.Now().UTC()

	// Build a parameterised multi-row INSERT.
	// PostgreSQL supports: INSERT INTO t (cols) VALUES ($1,$2,...), ($3,$4,...), ...
	placeholders := make([]string, 0, len(events))
	args := make([]interface{}, 0, len(events)*11)

	for i, event := range events {
		if event.EventID == "" {
			return fmt.Errorf("insert batch audit log: event at index %d has empty event_id", i)
		}
		if event.EventType == "" {
			return fmt.Errorf("insert batch audit log: event %s has empty event_type", event.EventID)
		}
		if err := model.ValidateActorType(event.ActorType); err != nil {
			return fmt.Errorf("insert batch audit log: event %s: %w", event.EventID, err)
		}

		if event.CreatedAt.IsZero() {
			event.CreatedAt = now
		}

		base := i * 11
		placeholders = append(placeholders, fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11,
		))
		args = append(args,
			event.EventID,
			event.EventType,
			event.ActorType,
			event.ActorID,
			event.ResourceType,
			event.ResourceID,
			event.Action,
			event.Details,
			event.IPAddress,
			event.UserAgent,
			event.CreatedAt,
		)
	}

	stmt := fmt.Sprintf(`INSERT INTO audit_log
		(event_id, event_type, actor_type, actor_id, resource_type, resource_id, action, details, ip_address, user_agent, created_at)
		VALUES %s`, strings.Join(placeholders, ", "))

	_, err := tx.ExecContext(ctx, stmt, args...)
	if err != nil {
		return fmt.Errorf("insert batch audit log: %w", err)
	}
	return nil
}

// Query returns audit events matching the given filters.
// Results are ordered by created_at DESC (most recent first).
// Uses the read-only connection and does NOT require a transaction.
func (r *AuditRepository) Query(ctx context.Context, q model.AuditQuery) ([]*model.AuditEvent, error) {
	q.Sanitize()

	// Build dynamic WHERE clause.
	var conditions []string
	args := make([]interface{}, 0)
	paramIdx := 1

	if q.EventType != "" {
		conditions = append(conditions, fmt.Sprintf("event_type = $%d", paramIdx))
		args = append(args, q.EventType)
		paramIdx++
	}
	if q.ActorType != "" {
		conditions = append(conditions, fmt.Sprintf("actor_type = $%d", paramIdx))
		args = append(args, q.ActorType)
		paramIdx++
	}
	if q.ActorID != "" {
		conditions = append(conditions, fmt.Sprintf("actor_id = $%d", paramIdx))
		args = append(args, q.ActorID)
		paramIdx++
	}
	if q.ResourceType != "" {
		conditions = append(conditions, fmt.Sprintf("resource_type = $%d", paramIdx))
		args = append(args, q.ResourceType)
		paramIdx++
	}
	if q.ResourceID != "" {
		conditions = append(conditions, fmt.Sprintf("resource_id = $%d", paramIdx))
		args = append(args, q.ResourceID)
		paramIdx++
	}
	if q.Action != "" {
		conditions = append(conditions, fmt.Sprintf("action = $%d", paramIdx))
		args = append(args, q.Action)
		paramIdx++
	}
	if !q.From.IsZero() {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", paramIdx))
		args = append(args, q.From)
		paramIdx++
	}
	if !q.To.IsZero() {
		conditions = append(conditions, fmt.Sprintf("created_at <= $%d", paramIdx))
		args = append(args, q.To)
		paramIdx++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Add LIMIT and OFFSET as positional params.
	query := fmt.Sprintf(`SELECT id, event_id, event_type, actor_type, actor_id,
		resource_type, resource_id, action, details, ip_address, user_agent, created_at
		FROM audit_log
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, whereClause, paramIdx, paramIdx+1)
	args = append(args, q.Limit, q.Offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}
	defer rows.Close()

	var events []*model.AuditEvent
	for rows.Next() {
		e := &model.AuditEvent{}
		if err := rows.Scan(
			&e.ID, &e.EventID, &e.EventType, &e.ActorType, &e.ActorID,
			&e.ResourceType, &e.ResourceID, &e.Action, &e.Details,
			&e.IPAddress, &e.UserAgent, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit log row: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit log rows: %w", err)
	}

	return events, nil
}

// GetByEventID retrieves a single audit event by its event_id.
// Returns nil if no event is found.
func (r *AuditRepository) GetByEventID(ctx context.Context, eventID string) (*model.AuditEvent, error) {
	query := `SELECT id, event_id, event_type, actor_type, actor_id,
		resource_type, resource_id, action, details, ip_address, user_agent, created_at
		FROM audit_log WHERE event_id = $1`

	e := &model.AuditEvent{}
	err := r.db.QueryRowContext(ctx, query, eventID).Scan(
		&e.ID, &e.EventID, &e.EventType, &e.ActorType, &e.ActorID,
		&e.ResourceType, &e.ResourceID, &e.Action, &e.Details,
		&e.IPAddress, &e.UserAgent, &e.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get audit event %s: %w", eventID, err)
	}
	return e, nil
}

// GetRecent returns the most recent N audit events.
func (r *AuditRepository) GetRecent(ctx context.Context, limit int) ([]*model.AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	query := `SELECT id, event_id, event_type, actor_type, actor_id,
		resource_type, resource_id, action, details, ip_address, user_agent, created_at
		FROM audit_log
		ORDER BY created_at DESC
		LIMIT $1`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent audit events: %w", err)
	}
	defer rows.Close()

	var events []*model.AuditEvent
	for rows.Next() {
		e := &model.AuditEvent{}
		if err := rows.Scan(
			&e.ID, &e.EventID, &e.EventType, &e.ActorType, &e.ActorID,
			&e.ResourceType, &e.ResourceID, &e.Action, &e.Details,
			&e.IPAddress, &e.UserAgent, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan recent audit log row: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent audit log rows: %w", err)
	}

	return events, nil
}

// Count returns the total number of audit events matching the given filters.
func (r *AuditRepository) Count(ctx context.Context, q model.AuditQuery) (int64, error) {
	var conditions []string
	args := make([]interface{}, 0)
	paramIdx := 1

	if q.EventType != "" {
		conditions = append(conditions, fmt.Sprintf("event_type = $%d", paramIdx))
		args = append(args, q.EventType)
		paramIdx++
	}
	if q.ActorType != "" {
		conditions = append(conditions, fmt.Sprintf("actor_type = $%d", paramIdx))
		args = append(args, q.ActorType)
		paramIdx++
	}
	if q.ActorID != "" {
		conditions = append(conditions, fmt.Sprintf("actor_id = $%d", paramIdx))
		args = append(args, q.ActorID)
		paramIdx++
	}
	if q.ResourceType != "" {
		conditions = append(conditions, fmt.Sprintf("resource_type = $%d", paramIdx))
		args = append(args, q.ResourceType)
		paramIdx++
	}
	if q.ResourceID != "" {
		conditions = append(conditions, fmt.Sprintf("resource_id = $%d", paramIdx))
		args = append(args, q.ResourceID)
		paramIdx++
	}
	if q.Action != "" {
		conditions = append(conditions, fmt.Sprintf("action = $%d", paramIdx))
		args = append(args, q.Action)
		paramIdx++
	}
	if !q.From.IsZero() {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", paramIdx))
		args = append(args, q.From)
		paramIdx++
	}
	if !q.To.IsZero() {
		conditions = append(conditions, fmt.Sprintf("created_at <= $%d", paramIdx))
		args = append(args, q.To)
		paramIdx++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(`SELECT COUNT(*) FROM audit_log %s`, whereClause)

	var count int64
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count audit events: %w", err)
	}
	return count, nil
}

// GenerateID creates a random hex-encoded ID with the given prefix.
// Uses crypto/rand for unpredictability.
func GenerateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
