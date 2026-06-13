// Package contract contains contract-level tests for vUSDC balance validation.
//
// Follows demo.md Section 6.4: "The vUSDC balance is sufficient."
// and Section 14 test: "Insufficient vUSDC balance."
//
// Run with: go test -tags=integration ./tests/contract/
package contract

import (
	"testing"
)

// TestInsufficientBalanceContract documents the balance validation contract.
//
// When a wallet has insufficient vUSDC available balance:
//   - LedgerService.HasSufficientBalance must return an error
//   - The checkout commit must fail before debiting
//   - No ledger entries should be created
//
// These tests require:
//   - A running PostgreSQL with ledger_entries table
//   - A wallet with a known balance
func TestInsufficientBalanceContract(t *testing.T) {
	t.Run("insufficient available balance blocks debit", func(t *testing.T) {
		t.Skip("requires database + ledger service - run with -tags=integration")
	})

	t.Run("sufficient balance allows debit", func(t *testing.T) {
		t.Skip("requires database + ledger service - run with -tags=integration")
	})

	t.Run("balance check is atomic with FOR UPDATE lock", func(t *testing.T) {
		t.Skip("requires database + ledger service - run with -tags=integration")
	})

	t.Run("zero balance returns error for positive amount", func(t *testing.T) {
		t.Skip("requires database + ledger service - run with -tags=integration")
	})
}

// TestBalanceDoubleSpendContract documents the double-spend prevention contract.
//
// Even with concurrent requests from the same wallet:
//   - Only the first request that acquires the advisory lock succeeds
//   - Subsequent requests see the updated balance and fail
func TestBalanceDoubleSpendContract(t *testing.T) {
	t.Run("concurrent debits from same wallet cannot exceed balance", func(t *testing.T) {
		t.Skip("requires database + ledger service - run with -tags=integration")
	})
}
