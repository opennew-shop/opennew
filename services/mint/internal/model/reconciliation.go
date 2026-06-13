package model

import "time"

// ReconciliationResult holds the output of a single reserve reconciliation run.
//
// Invariant (demo.md §7.1):
//
//	total_internal_liability + pending_redemption <= confirmed_reserve_balance
//
// If Difference < 0 the invariant is violated and an alert is raised.
type ReconciliationResult struct {
	AssetSymbol             string    `json:"asset_symbol"`
	ReserveConfirmedBalance int64     `json:"reserve_confirmed_balance_minor"`
	InternalLiability       int64     `json:"internal_liability_minor"`
	PendingRedemption       int64     `json:"pending_redemption_minor"`
	Difference              int64     `json:"difference_minor"`
	IsBalanced              bool      `json:"is_balanced"`
	ReconciledAt            time.Time `json:"reconciled_at"`
	AlertMessage            string    `json:"alert_message,omitempty"`
}
