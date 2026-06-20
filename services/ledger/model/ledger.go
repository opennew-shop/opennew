package model

import internal "github.com/ancf-commerce/ancf/services/ledger/internal/model"

type LedgerEntry = internal.LedgerEntry
type LedgerTransaction = internal.LedgerTransaction
type LedgerBalance = internal.LedgerBalance

const (
	AccountUserAvailable     = internal.AccountUserAvailable
	AccountUserPending       = internal.AccountUserPending
	AccountMerchantPending   = internal.AccountMerchantPending
	AccountMerchantSettled   = internal.AccountMerchantSettled
	AccountPlatformFee       = internal.AccountPlatformFee
	AccountReserveLiability  = internal.AccountReserveLiability
	AccountRedemptionPending = internal.AccountRedemptionPending
	AccountMintPending       = internal.AccountMintPending
	AccountReserveAsset      = internal.AccountReserveAsset

	EntryTypePurchaseHold      = internal.EntryTypePurchaseHold
	EntryTypePurchaseSettle    = internal.EntryTypePurchaseSettle
	EntryTypePurchaseRefund    = internal.EntryTypePurchaseRefund
	EntryTypeMintCredit        = internal.EntryTypeMintCredit
	EntryTypeRedemptionDebit   = internal.EntryTypeRedemptionDebit
	EntryTypeRedemptionRelease = internal.EntryTypeRedemptionRelease
	EntryTypeFeeCollect        = internal.EntryTypeFeeCollect
	EntryTypeDepositConfirm    = internal.EntryTypeDepositConfirm
)

func ValidateBalance(entries []LedgerEntry) bool {
	return internal.ValidateBalance(entries)
}

func PurchaseHold(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return internal.PurchaseHold(txID, wallet, amountMinor, currency, referenceID)
}

func PurchaseSettle(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return internal.PurchaseSettle(txID, wallet, amountMinor, currency, referenceID)
}

func PurchaseRefund(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return internal.PurchaseRefund(txID, wallet, amountMinor, currency, referenceID)
}

func MintCredit(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return internal.MintCredit(txID, wallet, amountMinor, currency, referenceID)
}

func RedemptionDebit(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return internal.RedemptionDebit(txID, wallet, amountMinor, currency, referenceID)
}

func RedemptionRelease(txID string, wallet string, amountMinor int64, currency string, referenceID string) []LedgerEntry {
	return internal.RedemptionRelease(txID, wallet, amountMinor, currency, referenceID)
}
