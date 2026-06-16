// Package service 提供审计服务的业务逻辑层，
// 在 repository 之上封装事件校验、独立事务写入与复用调用方事务的写入，
// 供审计 handler 及其他服务在自身事务边界内记录不可变审计事件。
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ancf-commerce/ancf/services/audit/internal/model"
	"github.com/ancf-commerce/ancf/services/audit/internal/repository"
)

// AuditService provides business-logic operations on top of the audit repository.
//
// It is designed to be called both from the audit handler itself and from
// other services that need to record immutable audit events within their
// own transaction boundaries.
type AuditService struct {
	repo *repository.AuditRepository
	db   *sql.DB
}

// NewAuditService creates a new AuditService.
func NewAuditService(db *sql.DB, repo *repository.AuditRepository) *AuditService {
	return &AuditService{
		repo: repo,
		db:   db,
	}
}

// RecordEvent validates and writes a single audit event inside a new short-lived
// transaction. Use RecordEventInTx when the caller already manages a transaction.
//
// Returns the created event with its generated event_id and created_at populated.
func (s *AuditService) RecordEvent(ctx context.Context, event *model.AuditEvent) (*model.AuditEvent, error) {
	if event.EventID == "" {
		event.EventID = repository.GenerateID("audit_")
	}

	if err := s.validateEvent(event); err != nil {
		return nil, fmt.Errorf("record audit event: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("record audit event: begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := s.repo.Insert(ctx, tx, event); err != nil {
		return nil, fmt.Errorf("record audit event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("record audit event: commit: %w", err)
	}

	return event, nil
}

// RecordEventInTx writes a single audit event inside the caller's existing
// transaction. This is the preferred method for services that need to atomically
// record an audit event alongside their own mutation.
//
// If event_id is empty, one is generated automatically.
// Returns the event with its generated fields populated.
func (s *AuditService) RecordEventInTx(ctx context.Context, tx *sql.Tx, event *model.AuditEvent) (*model.AuditEvent, error) {
	if event.EventID == "" {
		event.EventID = repository.GenerateID("audit_")
	}

	if err := s.validateEvent(event); err != nil {
		return nil, fmt.Errorf("record audit event in tx: %w", err)
	}

	if err := s.repo.Insert(ctx, tx, event); err != nil {
		return nil, fmt.Errorf("record audit event in tx: %w", err)
	}

	return event, nil
}

// QueryEvents returns audit events matching the given filters.
func (s *AuditService) QueryEvents(ctx context.Context, q model.AuditQuery) ([]*model.AuditEvent, int64, error) {
	q.Sanitize()

	events, err := s.repo.Query(ctx, q)
	if err != nil {
		return nil, 0, fmt.Errorf("query audit events: %w", err)
	}

	count, err := s.repo.Count(ctx, q)
	if err != nil {
		return nil, 0, fmt.Errorf("count audit events: %w", err)
	}

	return events, count, nil
}

// GetEvent retrieves a single audit event by its event_id.
func (s *AuditService) GetEvent(ctx context.Context, eventID string) (*model.AuditEvent, error) {
	event, err := s.repo.GetByEventID(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("get audit event: %w", err)
	}
	return event, nil
}

// GetRecent returns the most recent N audit events.
func (s *AuditService) GetRecent(ctx context.Context, limit int) ([]*model.AuditEvent, error) {
	events, err := s.repo.GetRecent(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent audit events: %w", err)
	}
	return events, nil
}

// RecordAdminEvent validates and writes an audit event submitted via the admin
// API. This method runs in its own transaction.
func (s *AuditService) RecordAdminEvent(ctx context.Context, req *model.AdminAuditRequest) (*model.AuditEvent, error) {
	event := &model.AuditEvent{
		EventID:      repository.GenerateID("audit_"),
		EventType:    req.EventType,
		ActorType:    req.ActorType,
		ResourceType: req.ResourceType,
		Action:       req.Action,
		Details:      req.Details,
	}

	if req.ActorID != "" {
		event.ActorID = sql.NullString{String: req.ActorID, Valid: true}
	}
	if req.ResourceID != "" {
		event.ResourceID = sql.NullString{String: req.ResourceID, Valid: true}
	}

	return s.RecordEvent(ctx, event)
}

// validateEvent checks that all required fields on an AuditEvent are populated
// and that enumerated values are within their allowed sets.
func (s *AuditService) validateEvent(event *model.AuditEvent) error {
	if event.EventID == "" {
		return fmt.Errorf("event_id is required")
	}
	if event.EventType == "" {
		return fmt.Errorf("event_type is required")
	}
	if event.ResourceType == "" {
		return fmt.Errorf("resource_type is required")
	}
	if event.Action == "" {
		return fmt.Errorf("action is required")
	}
	if err := model.ValidateActorType(event.ActorType); err != nil {
		return err
	}
	return nil
}

// mustMarshalJSON marshals v to JSON or returns an empty JSON object on error.
// Exported so other services can use it when building audit event details.
func MustMarshalJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(data)
}
