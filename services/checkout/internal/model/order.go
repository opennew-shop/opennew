package model

import (
	"encoding/json"
	"time"
)

// OrderIntent represents an order intention in the checkout pipeline.
// It maps to the order_intents table in PostgreSQL.
type OrderIntent struct {
	IntentID        string          `json:"intent_id" db:"intent_id"`
	QuoteID         string          `json:"quote_id" db:"quote_id"`
	Wallet          string          `json:"wallet" db:"wallet"`
	Network         string          `json:"network" db:"network"`
	Currency        string          `json:"currency" db:"currency"`
	TotalMinor      int64           `json:"total_minor" db:"total_minor"`
	Status          string          `json:"status" db:"status"`
	IdempotencyKey  *string         `json:"idempotency_key,omitempty" db:"idempotency_key"`
	WalletSignature *string         `json:"wallet_signature,omitempty" db:"wallet_signature"`
	AgentSessionID  *string         `json:"agent_session_id,omitempty" db:"agent_session_id"`
	Nonce           *string         `json:"nonce,omitempty" db:"nonce"`
	SignablePayload json.RawMessage `json:"signable_payload" db:"signable_payload"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
}

// Order intent status constants.
const (
	StatusCreated      = "created"
	StatusPrepared     = "prepared"
	StatusCommitted    = "committed"
	StatusPaid         = "paid"
	StatusProvisioning = "provisioning"
	StatusCompleted    = "completed"
	StatusFailed       = "failed"
	StatusRefunded     = "refunded"
)

// SignablePayload is the canonical order intent payload that the user wallet must sign.
// It is embedded in the checkout prepare response and stored with the order intent.
type SignablePayload struct {
	Domain     string `json:"domain"`
	ShopID     string `json:"shop_id"`
	Network    string `json:"network"`
	Wallet     string `json:"wallet"`
	QuoteID    string `json:"quote_id"`
	TotalMinor string `json:"total_minor"`
	Currency   string `json:"currency"`
	ExpiresAt  string `json:"expires_at"`
	Nonce      string `json:"nonce"`
}

// PrepareRequest is the request body for POST /api/v1/cli/checkout/prepare.
type PrepareRequest struct {
	QuoteID        string `json:"quote_id"`
	Wallet         string `json:"wallet"`
	Network        string `json:"network"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	// SECURITY FIX: F-001-01 — Optional idempotency key for prepare replay.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// PrepareResponse is the response body for POST /api/v1/cli/checkout/prepare.
type PrepareResponse struct {
	OrderIntentID   string           `json:"order_intent_id"`
	QuoteID         string           `json:"quote_id"`
	SignablePayload *SignablePayload `json:"signable_payload"`
}

// CommitRequest is the request body for POST /api/v1/cli/checkout/commit.
type CommitRequest struct {
	OrderIntentID   string `json:"order_intent_id"`
	QuoteID         string `json:"quote_id"`
	Wallet          string `json:"wallet"`
	WalletSignature string `json:"wallet_signature"`
	AgentSessionID  string `json:"agent_session_id"`
}

// CommitResponse is the response body for POST /api/v1/cli/checkout/commit.
type CommitResponse struct {
	OrderID       string `json:"order_id"`
	Status        string `json:"status"`
	OrderIntentID string `json:"order_intent_id"`
	CreatedAt     string `json:"created_at"`
}

// CachedResponse represents a cached idempotency response from the database.
type CachedResponse struct {
	StatusCode   int
	ResponseBody string
}
