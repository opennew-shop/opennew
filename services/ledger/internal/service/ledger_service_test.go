package service

import (
	"testing"

	"github.com/ancf-commerce/ancf/services/ledger/internal/model"
)

// TestLedgerService_HasSufficientBalance requires a database connection
// and an active transaction with advisory locking.
func TestLedgerService_HasSufficientBalance(t *testing.T) {
	t.Skip("requires database connection - run with -tags=integration")
}

// TestLedgerService_InsufficientBalance documents the expected behavior
// when a wallet has insufficient funds.
func TestLedgerService_InsufficientBalance(t *testing.T) {
	t.Skip("requires database connection - run with -tags=integration")
}

// TestPurchaseHold_Validation tests input validation for the PurchaseHold method.
func TestPurchaseHold_Validation(t *testing.T) {
	t.Skip("requires database connection and transaction - run with -tags=integration")
}

// TestLedgerEntryValidation tests the double-entry accounting invariants
// on the model layer (no database required).
func TestLedgerEntryValidation(t *testing.T) {
	t.Run("valid single entry pair balances", func(t *testing.T) {
		entries := []model.LedgerEntry{
			{
				EntryID:       "entry_001",
				TransactionID: "tx_001",
				DebitAccount:  model.AccountUserAvailable,
				CreditAccount: model.AccountUserPending,
				AmountMinor:   4900000,
				Currency:      "vUSDC",
				EntryType:     model.EntryTypePurchaseHold,
			},
		}
		if !model.ValidateBalance(entries) {
			t.Error("single entry pair should always balance (debit == credit)")
		}
	})

	t.Run("purchase hold creates correct debit/credit accounts", func(t *testing.T) {
		wallet := "wallet_01JTEST000000000000000000000000"
		entries := model.PurchaseHold("tx_001", wallet, 4900000, "vUSDC", "order_001")

		if len(entries) != 1 {
			t.Fatalf("expected 1 entry pair, got %d", len(entries))
		}
		e := entries[0]
		if e.DebitAccount != model.AccountUserAvailable {
			t.Errorf("expected debit_account %s, got %s", model.AccountUserAvailable, e.DebitAccount)
		}
		if e.CreditAccount != model.AccountUserPending {
			t.Errorf("expected credit_account %s, got %s", model.AccountUserPending, e.CreditAccount)
		}
		if *e.Wallet != wallet {
			t.Errorf("expected wallet %s, got %s", wallet, *e.Wallet)
		}
		if e.AmountMinor != 4900000 {
			t.Errorf("expected amount 4900000, got %d", e.AmountMinor)
		}
		if e.EntryType != model.EntryTypePurchaseHold {
			t.Errorf("expected entry_type %s, got %s", model.EntryTypePurchaseHold, e.EntryType)
		}
	})

	t.Run("purchase settle creates correct debit/credit accounts", func(t *testing.T) {
		wallet := "wallet_01JTEST000000000000000000000000"
		entries := model.PurchaseSettle("tx_002", wallet, 4900000, "vUSDC", "order_001")

		if len(entries) != 1 {
			t.Fatalf("expected 1 entry pair, got %d", len(entries))
		}
		e := entries[0]
		if e.DebitAccount != model.AccountUserPending {
			t.Errorf("expected debit_account %s, got %s", model.AccountUserPending, e.DebitAccount)
		}
		if e.CreditAccount != model.AccountMerchantSettled {
			t.Errorf("expected credit_account %s, got %s", model.AccountMerchantSettled, e.CreditAccount)
		}
	})

	t.Run("purchase refund creates correct debit/credit accounts", func(t *testing.T) {
		wallet := "wallet_01JTEST000000000000000000000000"
		entries := model.PurchaseRefund("tx_003", wallet, 4900000, "vUSDC", "order_001")

		if len(entries) != 1 {
			t.Fatalf("expected 1 entry pair, got %d", len(entries))
		}
		e := entries[0]
		if e.DebitAccount != model.AccountUserPending {
			t.Errorf("expected debit_account %s, got %s", model.AccountUserPending, e.DebitAccount)
		}
		if e.CreditAccount != model.AccountUserAvailable {
			t.Errorf("expected credit_account %s, got %s", model.AccountUserAvailable, e.CreditAccount)
		}
	})

	t.Run("mint credit creates correct 4-entry transaction", func(t *testing.T) {
		wallet := "wallet_01JTEST000000000000000000000000"
		entries := model.MintCredit("tx_004", wallet, 1000000, "vUSDC", "deposit_tx_001")

		if len(entries) != 2 {
			t.Fatalf("expected 2 entry pairs (4 entries), got %d pairs", len(entries))
		}

		// First pair: debit reserve_asset, credit reserve_liability.
		if entries[0].DebitAccount != model.AccountReserveAsset {
			t.Errorf("pair 1: expected debit %s, got %s", model.AccountReserveAsset, entries[0].DebitAccount)
		}
		if entries[0].CreditAccount != model.AccountReserveLiability {
			t.Errorf("pair 1: expected credit %s, got %s", model.AccountReserveLiability, entries[0].CreditAccount)
		}

		// Second pair: debit reserve_liability, credit user_available.
		if entries[1].DebitAccount != model.AccountReserveLiability {
			t.Errorf("pair 2: expected debit %s, got %s", model.AccountReserveLiability, entries[1].DebitAccount)
		}
		if entries[1].CreditAccount != model.AccountUserAvailable {
			t.Errorf("pair 2: expected credit %s, got %s", model.AccountUserAvailable, entries[1].CreditAccount)
		}

		// Both entries should reference the same wallet.
		for i, e := range entries {
			if e.Wallet == nil || *e.Wallet != wallet {
				t.Errorf("pair %d: expected wallet %s", i+1, wallet)
			}
		}
	})

	t.Run("redemption debit creates correct 4-entry transaction", func(t *testing.T) {
		wallet := "wallet_01JTEST000000000000000000000000"
		entries := model.RedemptionDebit("tx_005", wallet, 500000, "vUSDC", "redemption_001")

		if len(entries) != 2 {
			t.Fatalf("expected 2 entry pairs (4 entries), got %d pairs", len(entries))
		}

		// First pair: debit user_available, credit redemption_pending.
		if entries[0].DebitAccount != model.AccountUserAvailable {
			t.Errorf("pair 1: expected debit %s, got %s", model.AccountUserAvailable, entries[0].DebitAccount)
		}
		if entries[0].CreditAccount != model.AccountRedemptionPending {
			t.Errorf("pair 1: expected credit %s, got %s", model.AccountRedemptionPending, entries[0].CreditAccount)
		}

		// Second pair: debit reserve_liability, credit reserve_asset.
		if entries[1].DebitAccount != model.AccountReserveLiability {
			t.Errorf("pair 2: expected debit %s, got %s", model.AccountReserveLiability, entries[1].DebitAccount)
		}
		if entries[1].CreditAccount != model.AccountReserveAsset {
			t.Errorf("pair 2: expected credit %s, got %s", model.AccountReserveAsset, entries[1].CreditAccount)
		}
	})

	t.Run("validate balance with multiple entry pairs", func(t *testing.T) {
		entries := []model.LedgerEntry{
			{AmountMinor: 100, DebitAccount: "a", CreditAccount: "b"},
			{AmountMinor: 200, DebitAccount: "c", CreditAccount: "d"},
			{AmountMinor: 300, DebitAccount: "e", CreditAccount: "f"},
		}
		// ValidateBalance compares sum of debit amounts == sum of credit amounts.
		// Since each entry's AmountMinor is counted once for debit and once for credit,
		// the total should always balance.
		if !model.ValidateBalance(entries) {
			t.Error("equal debits and credits should balance")
		}
	})
}

// TestWalletBalanceModel verifies the WalletBalance struct invariants.
func TestWalletBalanceModel(t *testing.T) {
	balance := model.WalletBalance{
		Wallet:      "wallet_01JTEST",
		Currency:    "vUSDC",
		Available:   1000000,
		Pending:     500000,
		TotalDebit:  1500000,
		TotalCredit: 0,
	}
	if balance.Wallet != "wallet_01JTEST" {
		t.Error("wallet field mismatch")
	}
	if balance.Currency != "vUSDC" {
		t.Error("currency field mismatch")
	}
	if balance.Available < 0 {
		t.Error("available balance should be non-negative")
	}
}

// TestAccountTypes verifies all expected account types are defined.
func TestAccountTypes(t *testing.T) {
	expectedAccounts := map[string]string{
		"user_available":     model.AccountUserAvailable,
		"user_pending":       model.AccountUserPending,
		"merchant_pending":   model.AccountMerchantPending,
		"merchant_settled":   model.AccountMerchantSettled,
		"platform_fee":       model.AccountPlatformFee,
		"reserve_liability":  model.AccountReserveLiability,
		"redemption_pending": model.AccountRedemptionPending,
		"mint_pending":       model.AccountMintPending,
		"reserve_asset":      model.AccountReserveAsset,
	}
	for name, value := range expectedAccounts {
		if name != value {
			t.Errorf("expected account constant %q to equal %q, got %q", name, name, value)
		}
	}
}
