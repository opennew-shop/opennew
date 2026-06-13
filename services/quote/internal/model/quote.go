package model

import (
	"encoding/json"
	"time"
)

// DefaultQuoteCurrency is the default currency for quotes (AgentPay ticker).
const DefaultQuoteCurrency = "AGP"

// Quote represents a server-authoritative price quote.
// Quotes are short-lived and single-use only. Once consumed, they cannot be reused.
// It maps to the quotes table in PostgreSQL.
type Quote struct {
	ID         int64           `json:"id" db:"id"`
	QuoteID    string          `json:"quote_id" db:"quote_id"`
	Wallet     string          `json:"wallet" db:"wallet"`
	Network    string          `json:"network" db:"network"`
	Currency   string          `json:"currency" db:"currency"`
	TotalMinor int64           `json:"total_minor" db:"total_minor"`
	Scale      int             `json:"scale" db:"scale"`
	ExpiresAt  time.Time       `json:"expires_at" db:"expires_at"`
	Consumed   bool            `json:"consumed" db:"consumed"`
	ConsumedAt *time.Time      `json:"consumed_at,omitempty" db:"consumed_at"`
	Lines      json.RawMessage `json:"lines" db:"lines"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
}

// QuoteResponse is the API response shape for a quote request.
type QuoteResponse struct {
	QuoteID    string          `json:"quote_id"`
	Currency   string          `json:"currency"`
	TotalMinor string          `json:"total_minor"`
	Scale      int             `json:"scale"`
	ExpiresAt  time.Time       `json:"expires_at"`
	Lines      json.RawMessage `json:"lines"`
}

// QuoteLine represents a single line item within a quote.
type QuoteLine struct {
	SkuID          string `json:"sku_id"`
	Quantity       int    `json:"quantity"`
	UnitPriceMinor string `json:"unit_price_minor"`
	LineTotalMinor string `json:"line_total_minor"`
}

// QuoteRequest is the API request shape for creating a quote.
type QuoteRequest struct {
	Wallet  string              `json:"wallet"`
	Network string              `json:"network"`
	Lines   []QuoteRequestLine  `json:"lines"`
}

// QuoteRequestLine is a single line item in a quote request.
type QuoteRequestLine struct {
	SkuID    string `json:"sku_id"`
	Quantity int    `json:"quantity"`
}
