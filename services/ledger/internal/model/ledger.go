package model

import (
	"encoding/json"
	"time"
)

// LedgerEntry represents one side of a double-entry accounting transaction.
// Every operation that moves value creates at least two ledger_entries rows.
// This is the immutable source of truth; balances are derived materialized views.
// It maps to the ledger_entries table in PostgreSQL.
type LedgerEntry struct {
	ID            int64           `json:"id" db:"id"`
	EntryID       string          `json:"entry_id" db:"entry_id"`
	TransactionID string          `json:"transaction_id" db:"transaction_id"`
	Wallet        *string         `json:"wallet,omitempty" db:"wallet"`
	DebitAccount  string          `json:"debit_account" db:"debit_account"`
	CreditAccount string          `json:"credit_account" db:"credit_account"`
	AmountMinor   int64           `json:"amount_minor" db:"amount_minor"`
	Currency      string          `json:"currency" db:"currency"`
	EntryType     string          `json:"entry_type" db:"entry_type"`
	ReferenceID   *string         `json:"reference_id,omitempty" db:"reference_id"`
	Metadata      json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
}

// Account types used in the double-entry ledger.
// These map to the CHECK constraints on debit_account / credit_account columns.
const (
	AccountUserAvailable       = "user_available"
	AccountUserPending         = "user_pending"
	AccountMerchantPending     = "merchant_pending"
	AccountMerchantSettled     = "merchant_settled"
	AccountPlatformFee         = "platform_fee"
	AccountReserveLiability    = "reserve_liability"
	AccountRedemptionPending   = "redemption_pending"
	AccountMintPending         = "mint_pending"
	AccountReserveAsset        = "reserve_asset"
)

// Entry types used for transaction classification.
const (
	EntryTypePurchaseHold    = "purchase_hold"
	EntryTypePurchaseSettle  = "purchase_settle"
	EntryTypePurchaseRefund  = "purchase_refund"
	EntryTypeMintCredit      = "mint_credit"
	EntryTypeRedemptionDebit  = "redemption_debit"
	EntryTypeRedemptionRelease = "redemption_release"
	EntryTypeFeeCollect        = "fee_collect"
	EntryTypeDepositConfirm    = "deposit_confirm"
)

// LedgerTransaction represents a complete double-entry transaction consisting
// of one or more entry pairs. Used for validation before writing to the database.
type LedgerTransaction struct {
	TransactionID string        `json:"transaction_id"`
	Wallet        string        `json:"wallet,omitempty"`
	Currency      string        `json:"currency"`
	EntryType     string        `json:"entry_type"`
	Entries       []LedgerEntry `json:"entries"`
}

// LedgerBalance represents a materialized or computed balance for a specific
// account type and wallet.
type LedgerBalance struct {
	Wallet          string `json:"wallet"`
	AccountType     string `json:"account_type"`
	Currency        string `json:"currency"`
	BalanceMinor    int64  `json:"balance_minor"`
	LastEntryAt     time.Time `json:"last_entry_at,omitempty"`
}

// WalletBalance represents an aggregate balance view for a wallet across all account types
// within a single currency. It is derived from ledger_entries rather than stored directly.
type WalletBalance struct {
	Wallet       string `json:"wallet"`
	Currency     string `json:"currency"`
	Available    int64  `json:"available"`
	Pending      int64  `json:"pending"`
	TotalDebit   int64  `json:"total_debit"`
	TotalCredit  int64  `json:"total_credit"`
}

// ValidateBalance checks that total debits equal total credits in a list of entries.
// This is a fundamental invariant of double-entry accounting.
func ValidateBalance(entries []LedgerEntry) bool {
	var totalDebit, totalCredit int64
	for _, e := range entries {
		totalDebit += e.AmountMinor
		totalCredit += e.AmountMinor
	}
	return totalDebit == totalCredit
}

// PurchaseHold creates ledger entries for placing a purchase hold:
//
//	debit  user_available
//	credit user_pending
func PurchaseHold(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return []LedgerEntry{
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountUserAvailable,
			CreditAccount: AccountUserPending,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypePurchaseHold,
			ReferenceID:   &referenceID,
		},
	}
}

// PurchaseSettle creates ledger entries for settling a successful purchase:
//
//	debit  user_pending
//	credit merchant_settled
func PurchaseSettle(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return []LedgerEntry{
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountUserPending,
			CreditAccount: AccountMerchantSettled,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypePurchaseSettle,
			ReferenceID:   &referenceID,
		},
	}
}

// PurchaseRefund creates ledger entries for refunding a failed purchase:
//
//	debit  user_pending
//	credit user_available
func PurchaseRefund(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return []LedgerEntry{
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountUserPending,
			CreditAccount: AccountUserAvailable,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypePurchaseRefund,
			ReferenceID:   &referenceID,
		},
	}
}

// MintCredit creates ledger entries for crediting AgentPay (AGP) after a confirmed deposit:
//
//	debit  reserve_asset
//	credit reserve_liability
//	credit user_available
func MintCredit(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return []LedgerEntry{
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountReserveAsset,
			CreditAccount: AccountReserveLiability,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypeMintCredit,
			ReferenceID:   &referenceID,
		},
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountReserveLiability,
			CreditAccount: AccountUserAvailable,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypeMintCredit,
			ReferenceID:   &referenceID,
		},
	}
}

// RedemptionDebit creates ledger entries for processing an AgentPay (AGP) redemption:
//
//	debit  user_available
//	credit redemption_pending
//	debit  reserve_liability
//	credit reserve_asset
func RedemptionDebit(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return []LedgerEntry{
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountUserAvailable,
			CreditAccount: AccountRedemptionPending,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypeRedemptionDebit,
			ReferenceID:   &referenceID,
		},
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountReserveLiability,
			CreditAccount: AccountReserveAsset,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypeRedemptionDebit,
			ReferenceID:   &referenceID,
		},
	}
}

// RedemptionRelease creates ledger entries for releasing locked redemption funds
// back to the user (failure recovery path):
//
//	debit  redemption_pending
//	credit user_available
//	debit  reserve_asset
//	credit reserve_liability
func RedemptionRelease(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return []LedgerEntry{
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountRedemptionPending,
			CreditAccount: AccountUserAvailable,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypeRedemptionRelease,
			ReferenceID:   &referenceID,
		},
		{
			TransactionID: txID,
			Wallet:        &wallet,
			DebitAccount:  AccountReserveAsset,
			CreditAccount: AccountReserveLiability,
			AmountMinor:   amountMinor,
			Currency:      currency,
			EntryType:     EntryTypeRedemptionRelease,
			ReferenceID:   &referenceID,
		},
	}
}
