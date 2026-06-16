package model

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Redemption status constants
// ---------------------------------------------------------------------------

// 赎回状态常量 —— 定义 8 状态赎回状态机的各状态取值：created、balance_locked、
// burn_submitted、burned、payout_submitted、paid（正常路径终态），
// 以及 failed、released（失败回滚路径终态）。
const (
	RedemptionStatusCreated        = "created"
	RedemptionStatusBalanceLocked  = "balance_locked"
	RedemptionStatusBurnSubmitted  = "burn_submitted"
	RedemptionStatusBurned         = "burned"
	RedemptionStatusPayoutSubmitted = "payout_submitted"
	RedemptionStatusPaid           = "paid"
	RedemptionStatusFailed         = "failed"
	RedemptionStatusReleased       = "released"
)

// ---------------------------------------------------------------------------
// Status transition map
//
// Happy path:
//
//	created -> balance_locked -> burn_submitted -> burned -> payout_submitted -> paid
//
// Failure path (recoverable from any state):
//
//	any -> failed -> released
// ---------------------------------------------------------------------------

// RedemptionTransitions 定义赎回状态机的合法状态转移表，终态（paid/released）无出边。
var RedemptionTransitions = map[string][]string{
	RedemptionStatusCreated:          {RedemptionStatusBalanceLocked, RedemptionStatusFailed},
	RedemptionStatusBalanceLocked:    {RedemptionStatusBurnSubmitted, RedemptionStatusFailed},
	RedemptionStatusBurnSubmitted:    {RedemptionStatusBurned, RedemptionStatusFailed},
	RedemptionStatusBurned:           {RedemptionStatusPayoutSubmitted, RedemptionStatusFailed},
	RedemptionStatusPayoutSubmitted:  {RedemptionStatusPaid, RedemptionStatusFailed},
	RedemptionStatusFailed:           {RedemptionStatusReleased},
	RedemptionStatusReleased:         {}, // terminal
	RedemptionStatusPaid:             {}, // terminal
}

// ValidateRedemptionTransition checks whether a status transition is allowed.
func ValidateRedemptionTransition(from, to string) error {
	if from == to {
		return fmt.Errorf("redemption: transition from %s to %s is a no-op", from, to)
	}
	allowed, ok := RedemptionTransitions[from]
	if !ok {
		return fmt.Errorf("redemption: unknown status %s", from)
	}
	for _, dest := range allowed {
		if dest == to {
			return nil
		}
	}
	return fmt.Errorf("redemption: invalid transition from %s to %s", from, to)
}

// IsTerminalStatus returns true if the status represents a terminal state.
func IsTerminalStatus(status string) bool {
	transitions, ok := RedemptionTransitions[status]
	if !ok {
		return false
	}
	return len(transitions) == 0
}

// ---------------------------------------------------------------------------
// RedemptionRequest — maps to the redemption_requests table
// ---------------------------------------------------------------------------

// RedemptionRecord is the database-mapped row from redemption_requests.
type RedemptionRecord struct {
	ID          int64     `json:"id" db:"id"`
	RequestID   string    `json:"request_id" db:"request_id"`
	Wallet      string    `json:"wallet" db:"wallet"`
	AssetID     int64     `json:"asset_id" db:"asset_id"`
	AmountMinor int64     `json:"amount_minor" db:"amount_minor"`
	Status      string    `json:"status" db:"status"`
	BurnTxID    *string   `json:"burn_tx_id,omitempty" db:"burn_tx_id"`
	PayoutTxID  *string   `json:"payout_tx_id,omitempty" db:"payout_tx_id"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// ---------------------------------------------------------------------------
// API request / response structs
// ---------------------------------------------------------------------------

// CreateRedemptionRequest is the API payload for initiating a redemption.
type CreateRedemptionRequest struct {
	Wallet      string `json:"wallet" binding:"required"`
	Network     string `json:"network" binding:"required"`
	AssetSymbol string `json:"asset_symbol" binding:"required"`
	AmountMinor int64  `json:"amount_minor" binding:"required,gt=0"`
}

// RedemptionResponse is returned after creating or querying a redemption.
type RedemptionResponse struct {
	RequestID   string `json:"request_id"`
	Wallet      string `json:"wallet"`
	AssetSymbol string `json:"asset_symbol"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
	BurnTxID    string `json:"burn_tx_id,omitempty"`
	PayoutTxID  string `json:"payout_tx_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// PayoutRequest is the API payload for completing a payout.
type PayoutRequest struct {
	RequestID  string `json:"request_id" binding:"required"`
	PayoutTxID string `json:"payout_tx_id" binding:"required"`
}

// RedemptionStatusQuery is used to query redemption status by request_id.
type RedemptionStatusQuery struct {
	RequestID string `json:"request_id" binding:"required"`
}

// ReserveAccount maps to the reserve_accounts table.
// Shared by both mint and redemption flows.
type ReserveAccount struct {
	ID                    int64      `json:"id" db:"id"`
	Network               string     `json:"network" db:"network"`
	AssetSymbol           string     `json:"asset_symbol" db:"asset_symbol"`
	Address               string     `json:"address" db:"address"`
	ConfirmedBalanceMinor int64      `json:"confirmed_balance_minor" db:"confirmed_balance_minor"`
	PendingBalanceMinor   int64      `json:"pending_balance_minor" db:"pending_balance_minor"`
	LastReconciledAt      *time.Time `json:"last_reconciled_at,omitempty" db:"last_reconciled_at"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at" db:"updated_at"`
}
