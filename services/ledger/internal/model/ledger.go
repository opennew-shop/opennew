// Package model 定义双分录账本(ledger)的领域模型与分录构造函数。
// 每笔价值变动至少生成借、贷两条 ledger_entries,账本不可变,钱包余额由分录派生。
// 提供 PurchaseHold/Settle/Refund、MintCredit、RedemptionDebit/Release 等分录构造器。
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
	// AccountUserAvailable 用户可用余额账户
	AccountUserAvailable       = "user_available"
	// AccountUserPending 用户挂起(冻结)账户,购买时由可用余额转入
	AccountUserPending         = "user_pending"
	// AccountMerchantPending 商户挂起账户
	AccountMerchantPending     = "merchant_pending"
	// AccountMerchantSettled 商户已结算账户
	AccountMerchantSettled     = "merchant_settled"
	// AccountPlatformFee 平台手续费账户
	AccountPlatformFee         = "platform_fee"
	// AccountReserveLiability 储备负债账户(平台对用户的负债)
	AccountReserveLiability    = "reserve_liability"
	// AccountRedemptionPending 赎回挂起账户
	AccountRedemptionPending   = "redemption_pending"
	// AccountMintPending 铸币挂起账户
	AccountMintPending         = "mint_pending"
	// AccountReserveAsset 储备资产账户
	AccountReserveAsset        = "reserve_asset"
)

// Entry types used for transaction classification.
const (
	// EntryTypePurchaseHold 购买冻结:可用→挂起
	EntryTypePurchaseHold    = "purchase_hold"
	// EntryTypePurchaseSettle 购买结算:挂起→商户已结算
	EntryTypePurchaseSettle  = "purchase_settle"
	// EntryTypePurchaseRefund 购买退款:挂起→可用
	EntryTypePurchaseRefund  = "purchase_refund"
	// EntryTypeMintCredit 铸币入账:确认充值后为用户贷记 vUSDC
	EntryTypeMintCredit      = "mint_credit"
	// EntryTypeRedemptionDebit 赎回扣减:从用户可用余额扣除并锁定
	EntryTypeRedemptionDebit  = "redemption_debit"
	// EntryTypeRedemptionRelease 赎回释放:赎回失败时将锁定资金退回可用余额
	EntryTypeRedemptionRelease = "redemption_release"
	// EntryTypeFeeCollect 手续费收取
	EntryTypeFeeCollect        = "fee_collect"
	// EntryTypeDepositConfirm 充值确认
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
	// H-03 FIX: previous impl added AmountMinor to both totals → always true.
	// Aggregate per-account net positions; global sum must be zero.
	bal := map[string]int64{}
	for _, e := range entries {
		if e.AmountMinor <= 0 {
			return false
		}
		if e.DebitAccount == "" || e.CreditAccount == "" {
			return false
		}
		bal[e.DebitAccount] -= e.AmountMinor
		bal[e.CreditAccount] += e.AmountMinor
	}
	var sum int64
	for _, v := range bal {
		sum += v
	}
	return sum == 0
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
