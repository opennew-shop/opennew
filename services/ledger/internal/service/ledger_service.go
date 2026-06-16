// Package service 提供双分录账本的业务逻辑层。
// 所有 mutation 方法(PurchaseHold/Settle/Refund、MintCredit、RedemptionDebit/Release)
// 需调用方传入 *sql.Tx 以保证与其他领域操作的原子性;读取方法在事务外执行。
package service

import (
	"context"
	"database/sql"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/ancf-commerce/ancf/services/ledger/internal/model"
	"github.com/ancf-commerce/ancf/services/ledger/internal/repository"
)

// LedgerService provides the business-logic layer for double-entry ledger operations.
//
// All mutation methods (PurchaseHold, PurchaseSettle, PurchaseRefund, MintCredit,
// RedemptionDebit) require an explicit *sql.Tx. The caller owns the transaction
// lifecycle — this guarantees atomicity across ledger writes and other domain
// operations (see demo.md Section 11: Checkout Transaction Boundary).
//
// Read methods (GetBalance, GetEntries) operate outside a transaction and are
// suitable for display-only queries.
type LedgerService struct {
	repo *repository.LedgerRepository
	db   *sql.DB
}

// NewLedgerService creates a new LedgerService.
func NewLedgerService(repo *repository.LedgerRepository, db *sql.DB) *LedgerService {
	return &LedgerService{repo: repo, db: db}
}

// ---------------------------------------------------------------------------
// Mutation methods — all require a caller-managed *sql.Tx
// ---------------------------------------------------------------------------

// PurchaseHold places a hold on user funds during checkout.
//
// Double entry:
//
//	debit  user_available  (reduce available balance)
//	credit user_pending    (increase pending holds)
//
// The entry_type is "purchase_hold". All entries share a single transaction_id.
// referenceID is typically the order_intent_id.
func (s *LedgerService) PurchaseHold(ctx context.Context, tx *sql.Tx, wallet string, amountMinor int64, currency string, orderIntentID string) error {
	if amountMinor <= 0 {
		return fmt.Errorf("purchase_hold: amount_minor must be > 0, got %d", amountMinor)
	}
	if wallet == "" {
		return fmt.Errorf("purchase_hold: wallet is required")
	}

	txID := generateID("tx_")
	entries := model.PurchaseHold(txID, wallet, amountMinor, currency, orderIntentID)
	// Assign entry IDs for each entry
	for i := range entries {
		entries[i].EntryID = generateID("entry_")
	}
	return s.repo.PostTransaction(ctx, tx, entries)
}

// PurchaseSettle moves funds from pending to settled after a successful
// provisioning event.
//
// Double entry:
//
//	debit  user_pending      (reduce pending holds)
//	credit merchant_settled  (increase merchant settled balance)
//
// The entry_type is "purchase_settle". referenceID is typically the order_intent_id.
func (s *LedgerService) PurchaseSettle(ctx context.Context, tx *sql.Tx, wallet string, amountMinor int64, currency string, orderIntentID string) error {
	if amountMinor <= 0 {
		return fmt.Errorf("purchase_settle: amount_minor must be > 0, got %d", amountMinor)
	}
	if wallet == "" {
		return fmt.Errorf("purchase_settle: wallet is required")
	}

	txID := generateID("tx_")
	entries := model.PurchaseSettle(txID, wallet, amountMinor, currency, orderIntentID)
	for i := range entries {
		entries[i].EntryID = generateID("entry_")
	}
	return s.repo.PostTransaction(ctx, tx, entries)
}

// PurchaseRefund returns held funds to the user's available balance.
// Used when provisioning fails or the order is cancelled before settlement.
//
// Double entry:
//
//	debit  user_pending    (reduce pending holds)
//	credit user_available  (restore available balance)
//
// The entry_type is "purchase_refund". referenceID is typically the order_intent_id.
func (s *LedgerService) PurchaseRefund(ctx context.Context, tx *sql.Tx, wallet string, amountMinor int64, currency string, orderIntentID string) error {
	if amountMinor <= 0 {
		return fmt.Errorf("purchase_refund: amount_minor must be > 0, got %d", amountMinor)
	}
	if wallet == "" {
		return fmt.Errorf("purchase_refund: wallet is required")
	}

	txID := generateID("tx_")
	entries := model.PurchaseRefund(txID, wallet, amountMinor, currency, orderIntentID)
	for i := range entries {
		entries[i].EntryID = generateID("entry_")
	}
	return s.repo.PostTransaction(ctx, tx, entries)
}

// MintCredit credits vUSDC to a user after a confirmed reserve deposit.
//
// Double entry (two pairs, same transaction_id):
//
//	debit  reserve_asset       (increase reserve assets)
//	credit reserve_liability   (increase platform liability)
//
//	debit  reserve_liability   (reduce platform liability)
//	credit user_available      (increase user available balance)
//
// The entry_type is "mint_credit". referenceID is typically the deposit_tx_id
// from the chain-adapter or the mint_request_id.
func (s *LedgerService) MintCredit(ctx context.Context, tx *sql.Tx, wallet string, amountMinor int64, currency string, depositTxID string) error {
	if amountMinor <= 0 {
		return fmt.Errorf("mint_credit: amount_minor must be > 0, got %d", amountMinor)
	}
	if wallet == "" {
		return fmt.Errorf("mint_credit: wallet is required")
	}

	txID := generateID("tx_")
	entries := model.MintCredit(txID, wallet, amountMinor, currency, depositTxID)
	for i := range entries {
		entries[i].EntryID = generateID("entry_")
	}
	return s.repo.PostTransaction(ctx, tx, entries)
}

// RedemptionDebit processes a vUSDC redemption, debiting the user's available
// balance and reducing the reserve liability.
//
// Double entry (two pairs, same transaction_id):
//
//	debit  user_available       (reduce user available balance)
//	credit redemption_pending   (increase pending redemption)
//
//	debit  reserve_liability    (reduce platform liability)
//	credit reserve_asset        (reduce reserve assets)
//
// The entry_type is "redemption_debit". referenceID is typically the
// redemption_request_id.
func (s *LedgerService) RedemptionDebit(ctx context.Context, tx *sql.Tx, wallet string, amountMinor int64, currency string, redemptionID string) error {
	if amountMinor <= 0 {
		return fmt.Errorf("redemption_debit: amount_minor must be > 0, got %d", amountMinor)
	}
	if wallet == "" {
		return fmt.Errorf("redemption_debit: wallet is required")
	}

	txID := generateID("tx_")
	entries := model.RedemptionDebit(txID, wallet, amountMinor, currency, redemptionID)
	for i := range entries {
		entries[i].EntryID = generateID("entry_")
	}
	return s.repo.PostTransaction(ctx, tx, entries)
}

// RedemptionRelease releases locked redemption funds back to the user's available
// balance after a failed or cancelled redemption.
//
// Double entry (two pairs, same transaction_id):
//
//	debit  redemption_pending   (reduce pending redemption)
//	credit user_available       (restore user available balance)
//
//	debit  reserve_asset         (restore reserve assets)
//	credit reserve_liability     (restore platform liability)
//
// The entry_type is "redemption_release". referenceID is typically the
// redemption_request_id.
func (s *LedgerService) RedemptionRelease(ctx context.Context, tx *sql.Tx, wallet string, amountMinor int64, currency string, redemptionID string) error {
	if amountMinor <= 0 {
		return fmt.Errorf("redemption_release: amount_minor must be > 0, got %d", amountMinor)
	}
	if wallet == "" {
		return fmt.Errorf("redemption_release: wallet is required")
	}

	txID := generateID("tx_")
	entries := model.RedemptionRelease(txID, wallet, amountMinor, currency, redemptionID)
	for i := range entries {
		entries[i].EntryID = generateID("entry_")
	}
	return s.repo.PostTransaction(ctx, tx, entries)
}

// ---------------------------------------------------------------------------
// Read methods — operate outside a transaction
// ---------------------------------------------------------------------------

// GetBalance returns the aggregate wallet balance (available + pending) derived
// from ledger_entries. This is a display-only read; use HasSufficientBalance
// inside transactions for authoritative balance checks with locking.
func (s *LedgerService) GetBalance(ctx context.Context, wallet string, currency string) (*model.WalletBalance, error) {
	if wallet == "" {
		return nil, fmt.Errorf("get_balance: wallet is required")
	}
	return s.repo.GetBalance(ctx, wallet, currency)
}

// GetEntries returns paginated ledger entries for a wallet.
func (s *LedgerService) GetEntries(ctx context.Context, wallet string, limit, offset int) ([]model.LedgerEntry, error) {
	if wallet == "" {
		return nil, fmt.Errorf("get_entries: wallet is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.GetEntries(ctx, wallet, limit, offset)
}

// ---------------------------------------------------------------------------
// Transactional balance check
// ---------------------------------------------------------------------------

// HasSufficientBalance checks within a transaction whether a wallet has at
// least requiredAmountMinor available. It acquires a PostgreSQL advisory lock
// to serialise concurrent balance checks for the same wallet, preventing
// double-spend races.
//
// Returns nil if sufficient; returns an error describing the shortfall otherwise.
func (s *LedgerService) HasSufficientBalance(ctx context.Context, tx *sql.Tx, wallet string, currency string, requiredAmountMinor int64) error {
	if requiredAmountMinor <= 0 {
		return fmt.Errorf("has_sufficient_balance: required amount must be > 0, got %d", requiredAmountMinor)
	}
	if wallet == "" {
		return fmt.Errorf("has_sufficient_balance: wallet is required")
	}

	balance, err := s.repo.GetBalanceForUpdate(ctx, tx, wallet, currency)
	if err != nil {
		return fmt.Errorf("has_sufficient_balance: %w", err)
	}

	if balance.Available < requiredAmountMinor {
		return fmt.Errorf("insufficient balance: wallet %s has %d available (in minor units of %s), required %d",
			wallet, balance.Available, currency, requiredAmountMinor)
	}
	return nil
}

// generateID creates a random hex-encoded ID with the given prefix.
// Uses crypto/rand for unpredictability.
func generateID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
