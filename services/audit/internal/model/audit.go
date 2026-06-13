package model

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Actor type constants (maps to audit_log.actor_type CHECK constraint)
// ---------------------------------------------------------------------------
const (
	ActorTypeUser   = "user"
	ActorTypeAgent  = "agent"
	ActorTypeSystem = "system"
	ActorTypeAdmin  = "admin"
)

// ---------------------------------------------------------------------------
// Resource type constants (maps to audit_log.resource_type)
// ---------------------------------------------------------------------------
const (
	ResourceTypeOrder       = "order"
	ResourceTypeQuote       = "quote"
	ResourceTypeMint        = "mint"
	ResourceTypeRedemption  = "redemption"
	ResourceTypeLedger      = "ledger"
	ResourceTypeReserve     = "reserve"
	ResourceTypeAsset       = "asset"
	ResourceTypeCheckout    = "checkout"
	ResourceTypeCatalog     = "catalog_sku"
	ResourceTypeChainTx     = "chain_tx"
	ResourceTypeProvisioning = "provisioning"
	ResourceTypeReconciliation = "reconciliation"
)

// ---------------------------------------------------------------------------
// Action constants (maps to audit_log.action)
// ---------------------------------------------------------------------------
const (
	ActionCreated     = "created"
	ActionUpdated     = "updated"
	ActionDeleted     = "deleted"
	ActionExported    = "exported"
	ActionReconciled  = "reconciled"
	ActionConfirmed   = "confirmed"
	ActionFailed      = "failed"
	ActionCancelled   = "cancelled"
	ActionProcessed   = "processed"
	ActionApproved    = "approved"
	ActionRejected    = "rejected"
)

// ValidActorTypes is the set of allowed actor_type values as defined by the
// database CHECK constraint.
var ValidActorTypes = map[string]bool{
	ActorTypeUser:   true,
	ActorTypeAgent:  true,
	ActorTypeSystem: true,
	ActorTypeAdmin:  true,
}

// ValidateActorType returns an error if actorType is not in the allowed set.
func ValidateActorType(actorType string) error {
	if _, ok := ValidActorTypes[actorType]; !ok {
		return fmt.Errorf("invalid actor_type %q: must be one of user, agent, system, admin", actorType)
	}
	return nil
}

// ---------------------------------------------------------------------------
// AuditEvent — maps to the audit_log table (immutable)
// ---------------------------------------------------------------------------
type AuditEvent struct {
	ID           int64           `json:"id" db:"id"`
	EventID      string          `json:"event_id" db:"event_id"`
	EventType    string          `json:"event_type" db:"event_type"`
	ActorType    string          `json:"actor_type" db:"actor_type"`
	ActorID      sql.NullString  `json:"actor_id,omitempty" db:"actor_id"`
	ResourceType string          `json:"resource_type" db:"resource_type"`
	ResourceID   sql.NullString  `json:"resource_id,omitempty" db:"resource_id"`
	Action       string          `json:"action" db:"action"`
	Details      json.RawMessage `json:"details,omitempty" db:"details"`
	IPAddress    sql.NullString  `json:"ip_address,omitempty" db:"ip_address"`
	UserAgent    sql.NullString  `json:"user_agent,omitempty" db:"user_agent"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
}

// ---------------------------------------------------------------------------
// AuditQuery represents filterable parameters for querying the audit log.
// ---------------------------------------------------------------------------
type AuditQuery struct {
	EventType    string    `json:"event_type,omitempty" form:"event_type"`
	ActorType    string    `json:"actor_type,omitempty" form:"actor_type"`
	ActorID      string    `json:"actor_id,omitempty" form:"actor_id"`
	ResourceType string    `json:"resource_type,omitempty" form:"resource_type"`
	ResourceID   string    `json:"resource_id,omitempty" form:"resource_id"`
	Action       string    `json:"action,omitempty" form:"action"`
	From         time.Time `json:"from,omitempty" form:"from"`
	To           time.Time `json:"to,omitempty" form:"to"`
	Limit        int       `json:"limit,omitempty" form:"limit"`
	Offset       int       `json:"offset,omitempty" form:"offset"`
}

// Sanitize applies defaults and clamps the query parameters.
func (q *AuditQuery) Sanitize() {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	if q.To.IsZero() {
		q.To = time.Now().UTC()
	}
	if q.From.IsZero() {
		q.From = time.Time{} // zero time = no lower bound
	}
}

// ---------------------------------------------------------------------------
// AdminAuditRequest — request body for admin audit writes (if needed)
// ---------------------------------------------------------------------------
type AdminAuditRequest struct {
	EventType    string          `json:"event_type" binding:"required"`
	ActorType    string          `json:"actor_type" binding:"required"`
	ActorID      string          `json:"actor_id,omitempty"`
	ResourceType string          `json:"resource_type" binding:"required"`
	ResourceID   string          `json:"resource_id,omitempty"`
	Action       string          `json:"action" binding:"required"`
	Details      json.RawMessage `json:"details,omitempty"`
}
