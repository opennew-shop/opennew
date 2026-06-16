// Package model 定义 mint 服务的数据模型与状态机。
// 包含 9 状态铸币状态机（MintRequest）、8 状态赎回状态机（RedemptionRecord）、
// 影子账本 vUSDC/AGP 相关的储备账户（ReserveAccount）与铸币策略（MintPolicy），
// 以及储备对账结果（ReconciliationResult，满足
// total_liability + pending_redemption <= confirmed_reserve 不变式）。
package model

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// DefaultAgentPayCurrency is the default currency code for AgentPay (formerly vUSDC).
const DefaultAgentPayCurrency = "AGP"

// MintRequest status constants.
const (
	MintStatusCreated          = "created"
	MintStatusDepositConfirmed = "deposit_confirmed"
	MintStatusRiskChecking     = "risk_checking"
	MintStatusApproved         = "approved"
	MintStatusMintSubmitted    = "mint_submitted"
	MintStatusMinted           = "minted"
	MintStatusCredited         = "credited"
	MintStatusFailed           = "failed"
	MintStatusCancelled        = "cancelled"
)

// MintTransitions defines the allowed state transitions for a mint request.
// Terminal states (credited, failed, cancelled) have no outgoing transitions.
var MintTransitions = map[string][]string{
	MintStatusCreated:          {MintStatusDepositConfirmed, MintStatusCancelled},
	MintStatusDepositConfirmed: {MintStatusRiskChecking, MintStatusFailed},
	MintStatusRiskChecking:     {MintStatusApproved, MintStatusFailed},
	MintStatusApproved:         {MintStatusMintSubmitted, MintStatusFailed},
	MintStatusMintSubmitted:    {MintStatusMinted, MintStatusFailed},
	MintStatusMinted:           {MintStatusCredited, MintStatusFailed},
	MintStatusCredited:         {}, // terminal
	MintStatusFailed:           {}, // terminal
	MintStatusCancelled:        {}, // terminal
}

// ValidateMintTransition checks whether a state transition from current to target
// is legal according to MintTransitions.
func ValidateMintTransition(from, to string) error {
	if from == to {
		return nil
	}
	allowed, ok := MintTransitions[from]
	if !ok {
		return fmt.Errorf("unknown mint status %q", from)
	}
	if len(allowed) == 0 {
		return fmt.Errorf("status %q is terminal; cannot transition to %q", from, to)
	}
	for _, a := range allowed {
		if a == to {
			return nil
		}
	}
	return fmt.Errorf("invalid mint status transition from %q to %q", from, to)
}

// MintRequest maps to the mint_requests table.
type MintRequest struct {
	ID                 int64           `json:"id" db:"id"`
	RequestID          string          `json:"request_id" db:"request_id"`
	Wallet             string          `json:"wallet" db:"wallet"`
	AssetID            int64           `json:"asset_id" db:"asset_id"`
	ReserveDepositTxID *string         `json:"reserve_deposit_tx_id,omitempty" db:"reserve_deposit_tx_id"`
	AmountMinor        int64           `json:"amount_minor" db:"amount_minor"`
	Status             string          `json:"status" db:"status"`
	RiskScore          *float64        `json:"risk_score,omitempty" db:"risk_score"`
	ApprovalID         *string         `json:"approval_id,omitempty" db:"approval_id"`
	ChainMintTxID      *string         `json:"chain_mint_tx_id,omitempty" db:"chain_mint_tx_id"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
}

// MintPolicy maps to the mint_policies table.
type MintPolicy struct {
	ID                              int64     `json:"id" db:"id"`
	AssetID                         int64     `json:"asset_id" db:"asset_id"`
	DailyMintLimitMinor             int64     `json:"daily_mint_limit_minor" db:"daily_mint_limit_minor"`
	PerWalletLimitMinor             int64     `json:"per_wallet_limit_minor" db:"per_wallet_limit_minor"`
	RequireManualApprovalAboveMinor int64     `json:"require_manual_approval_above_minor" db:"require_manual_approval_above_minor"`
	Status                          string    `json:"status" db:"status"`
	CreatedAt                       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt                       time.Time `json:"updated_at" db:"updated_at"`
}

// Asset maps to the assets table (subset relevant to mint service).
type Asset struct {
	ID          int64     `json:"id" db:"id"`
	Symbol      string    `json:"symbol" db:"symbol"`
	Decimals    int       `json:"decimals" db:"decimals"`
	AssetType   string    `json:"asset_type" db:"asset_type"`
	Network     string    `json:"network" db:"network"`
	MintAddress *string   `json:"mint_address,omitempty" db:"mint_address"`
	Status      string    `json:"status" db:"status"`
}

// --- API request/response structs ---

// DepositIntentRequest is the request body for POST /api/v1/wallet/deposit-intents.
type DepositIntentRequest struct {
	Wallet      string `json:"wallet" binding:"required"`
	Network     string `json:"network" binding:"required"`
	AssetSymbol string `json:"asset_symbol" binding:"required"`
}

// DepositIntentResponse is the response for POST /api/v1/wallet/deposit-intents.
type DepositIntentResponse struct {
	DepositIntentID string `json:"deposit_intent_id"`
	ReserveAddress  string `json:"reserve_address"`
	Memo            string `json:"memo"`
}

// ConfirmDepositRequest is the request body for POST /api/v1/internal/deposit-confirm.
type ConfirmDepositRequest struct {
	DepositIntentID string `json:"deposit_intent_id" binding:"required"`
	DepositTxID     string `json:"deposit_tx_id" binding:"required"`
	AmountMinor     int64  `json:"amount_minor" binding:"required,gt=0"`
}

// ChainDepositProof is the raw_json payload persisted by chain-adapter for a finalized deposit.
type ChainDepositProof struct {
	Network         string `json:"network"`
	TxHash          string `json:"tx_hash"`
	FromAddress     string `json:"from_address"`
	ToAddress       string `json:"to_address"`
	AmountMinor     int64  `json:"amount_minor"`
	AssetSymbol     string `json:"asset_symbol"`
	DepositIntentID string `json:"deposit_intent_id,omitempty"`
	BlockNumber     int64  `json:"block_number"`
}

// MintStatusResponse is the response for GET /api/v1/wallet/mint-status.
type MintStatusResponse struct {
	RequestID          string  `json:"request_id"`
	Wallet             string  `json:"wallet"`
	AssetSymbol        string  `json:"asset_symbol"`
	AmountMinor        int64   `json:"amount_minor"`
	Status             string  `json:"status"`
	RiskScore          *float64 `json:"risk_score,omitempty"`
	ReserveDepositTxID *string `json:"reserve_deposit_tx_id,omitempty"`
	ChainMintTxID      *string `json:"chain_mint_tx_id,omitempty"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

// ReserveInfoResponse is the response for GET /api/v1/wallet/reserve-info.
type ReserveInfoResponse struct {
	Network               string `json:"network"`
	AssetSymbol           string `json:"asset_symbol"`
	ReserveAddress        string `json:"reserve_address"`
	ConfirmedBalanceMinor int64  `json:"confirmed_balance_minor"`
	PendingBalanceMinor   int64  `json:"pending_balance_minor"`
}

// AuditEvent represents an immutable audit log entry (maps to audit_log table).
type AuditEvent struct {
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	ActorType    string          `json:"actor_type"`
	ActorID      string          `json:"actor_id"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Action       string          `json:"action"`
	Details      json.RawMessage `json:"details"`
	IPAddress    sql.NullString  `json:"ip_address,omitempty"`
	UserAgent    sql.NullString  `json:"user_agent,omitempty"`
}
