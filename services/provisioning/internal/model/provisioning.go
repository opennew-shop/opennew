// Package model 定义服务开通的数据模型与常量：
// 服务类型 (算力租用/存储分配/API Key 签发)、开通状态、开通结果与请求，
// 以及监听/发出的 Outbox 事件类型和审计事件类型。
package model

import (
	"encoding/json"
	"time"
)

// Provisioning service type constants.
// Determined by SKU ID prefix analysis; maps to different provisioning strategies.
const (
	ServiceTypeComputeRental    = "compute_rental"
	ServiceTypeStorageAllocation = "storage_allocation"
	ServiceTypeAPIKeyIssuance   = "api_key_issuance"
)

// Provisioning request status constants.
// These map to the provisioning process lifecycle, distinct from order_intents.status
// but typically tracked alongside it during the provisioning phase.
const (
	ProvStatusPending       = "pending"
	ProvStatusProvisioning  = "provisioning"
	ProvStatusProvisioned   = "provisioned"
	ProvStatusFailed        = "failed"
)

// ProvisioningResult holds the outcome of a provisioning attempt.
// On success, it contains access credentials; on failure, it contains the error.
type ProvisioningResult struct {
	OrderIntentID string          `json:"order_intent_id"`
	SKUID         string          `json:"sku_id"`
	ServiceType   string          `json:"service_type"`
	Status        string          `json:"status"`
	AccessToken   *string         `json:"access_token,omitempty"`
	InstanceID    *string         `json:"instance_id,omitempty"`
	EndpointURL   *string         `json:"endpoint_url,omitempty"`
	ErrorMessage  *string         `json:"error_message,omitempty"`
	ProvisionedAt *time.Time      `json:"provisioned_at,omitempty"`
	Details       json.RawMessage `json:"details,omitempty"`
}

// ProvisioningRequest is the in-flight state of a provisioning operation.
// It is derived from the outbox order_committed event payload.
type ProvisioningRequest struct {
	OrderIntentID string `json:"order_intent_id"`
	OrderID       string `json:"order_id"`
	QuoteID       string `json:"quote_id"`
	Wallet        string `json:"wallet"`
	TotalMinor    int64  `json:"total_minor"`
	Currency      string `json:"currency"`
	LineCount     int    `json:"line_count"`
}

// ProvisionAccessResponse is returned to the user when they query for their access credentials
// after the provisioning has completed successfully.
type ProvisionAccessResponse struct {
	OrderIntentID string  `json:"order_intent_id"`
	Status        string  `json:"status"`
	ServiceType   string  `json:"service_type"`
	SKUID         string  `json:"sku_id"`
	AccessToken   *string `json:"access_token,omitempty"`
	InstanceID    *string `json:"instance_id,omitempty"`
	EndpointURL   *string `json:"endpoint_url,omitempty"`
	ProvisionedAt *string `json:"provisioned_at,omitempty"`
}

// ProvisionStatusResponse is returned for admin status queries.
type ProvisionStatusResponse struct {
	OrderIntentID string  `json:"order_intent_id"`
	OrderStatus   string  `json:"order_status"`
	ServiceType   string  `json:"service_type"`
	SKUID         string  `json:"sku_id"`
	AccessToken   *string `json:"access_token,omitempty"`
	InstanceID    *string `json:"instance_id,omitempty"`
	EndpointURL   *string `json:"endpoint_url,omitempty"`
	ProvisionedAt *string `json:"provisioned_at,omitempty"`
	ErrorMessage  *string `json:"error_message,omitempty"`
}

// OutboxEvent represents an outbox event from the outbox table.
// Mirrors the structure defined in checkout/internal/repository/outbox_repository.go.
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

// Outbox event type constants the provisioning service listens for.
const (
	EventTypeOrderCommitted = "order_committed"
)

// Provisioning event types emitted by this service into the outbox.
const (
	EventTypeProvisioningStarted   = "provisioning_started"
	EventTypeProvisioningCompleted = "provisioning_completed"
	EventTypeProvisioningFailed    = "provisioning_failed"
)

// Audit event type constants for provisioning operations.
const (
	AuditEventProvisioningStarted   = "provisioning.started"
	AuditEventProvisioningCompleted = "provisioning.completed"
	AuditEventProvisioningFailed    = "provisioning.failed"
)

// AuditEvent is a thin wrapper for an immutable audit log entry.
type AuditEvent struct {
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	ActorType    string          `json:"actor_type"`
	ActorID      string          `json:"actor_id"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Action       string          `json:"action"`
	Details      json.RawMessage `json:"details"`
}

// ManualProvisionRequest is the admin request body to manually trigger provisioning.
type ManualProvisionRequest struct {
	OrderIntentID string `json:"order_intent_id" binding:"required"`
}
