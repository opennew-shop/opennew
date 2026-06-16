package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ancf-commerce/ancf/services/mint/internal/model"
	"github.com/ancf-commerce/ancf/services/mint/internal/repository"
)

// ReconciliationService executes reserve-vs-liability reconciliation runs
// against the shadow-ledger vUSDC model.
//
// It is designed to be invoked manually (POST /api/v1/admin/reconcile) or
// from a scheduled cron job.
//
// Core invariant (demo.md 7.1 and 7.2):
//
//	total_internal_liability + pending_redemption <= confirmed_reserve_balance
//
// 中文说明：储备对账服务，针对影子账本 vUSDC 模型执行“储备 vs 负债”对账。
// 可经 POST /api/v1/admin/reconcile 手动触发或由定时任务调用，核验上述储备不变式。
type ReconciliationService struct {
	db       *sql.DB
	mintRepo *repository.MintRepository
}

// NewReconciliationService creates a new ReconciliationService.
func NewReconciliationService(db *sql.DB, mintRepo *repository.MintRepository) *ReconciliationService {
	return &ReconciliationService{
		db:       db,
		mintRepo: mintRepo,
	}
}

// lastResult is an in-memory cache of the most recent reconciliation result.
// In production this would be persisted (and potentially made queryable via API).
var lastResult *model.ReconciliationResult

// Reconcile runs a single reconciliation for the given asset symbol.
//
// Steps:
//  1. Query reserve_accounts.confirmed_balance_minor.
//  2. Compute internal liability = net reserve_liability from ledger_entries
//     (SUM credits - SUM debits for reserve_liability account).
//  3. Compute pending redemption = SUM(amount_minor) from redemption_requests
//     where status is NOT a terminal-complete state (paid/released/failed).
//  4. difference = confirmed_balance - (liability + pending)
//  5. If difference < 0, set IsBalanced=false and generate an alert message.
func (s *ReconciliationService) Reconcile(ctx context.Context, assetSymbol string) (*model.ReconciliationResult, error) {
	// 1. Look up the reserve account.
	//    We use the default network "solana-mainnet"; the caller may pass a
	//    network-qualified key in production.
	network := "solana-mainnet"
	reserve, err := s.mintRepo.GetReserveAccount(ctx, network, assetSymbol)
	if err != nil {
		return nil, fmt.Errorf("reconcile: reserve account lookup (network=%s asset=%s): %w",
			network, assetSymbol, err)
	}

	// 2. Compute internal liability: net position of the reserve_liability account
	//    in the ledger_entries table.
	//
	//    Positive entries are credits to reserve_liability (increase liability).
	//    Negative entries are debits from reserve_liability (decrease liability,
	//    e.g. during redemption).
	//
	//    SQL: SUM(CASE WHEN credit_account='reserve_liability' THEN amount_minor
	//                  WHEN debit_account='reserve_liability'  THEN -amount_minor
	//                  ELSE 0 END)
	var internalLiability int64
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(
			CASE WHEN credit_account = 'reserve_liability' THEN amount_minor
			     WHEN debit_account  = 'reserve_liability' THEN -amount_minor
			     ELSE 0 END
		), 0)
		FROM ledger_entries
		WHERE (credit_account = 'reserve_liability' OR debit_account = 'reserve_liability')
		  AND currency = $1
	`, assetSymbol).Scan(&internalLiability)
	if err != nil {
		return nil, fmt.Errorf("reconcile: compute internal liability: %w", err)
	}

	// 3. Compute pending redemption: SUM of redemption_requests that have not
	//    yet reached a terminal-complete state.
	//
	//    Terminal states that reduce the redemption exposure are:
	//      - paid    (payout completed, reserve has been debited)
	//      - released (funds returned to user, no further liability)
	//      - failed  (request cancelled, no payout will occur)
	//
	//    All other statuses (created, balance_locked, burn_submitted, burned,
	//    payout_submitted) represent an outstanding obligation against the reserve.
	var pendingRedemption int64
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_minor), 0)
		FROM redemption_requests
		WHERE status NOT IN ('paid', 'released', 'failed')
	`, nil).Scan(&pendingRedemption)
	if err != nil {
		return nil, fmt.Errorf("reconcile: compute pending redemption: %w", err)
	}

	// 4. Compute difference.
	//    diff >= 0 means reserves exceed or equal total obligations (healthy).
	//    diff < 0 means reserve deficit — invariant violated.
	diff := reserve.ConfirmedBalanceMinor - (internalLiability + pendingRedemption)

	result := &model.ReconciliationResult{
		AssetSymbol:             assetSymbol,
		ReserveConfirmedBalance: reserve.ConfirmedBalanceMinor,
		InternalLiability:       internalLiability,
		PendingRedemption:       pendingRedemption,
		Difference:              diff,
		IsBalanced:              diff >= 0,
		ReconciledAt:            time.Now().UTC(),
	}

	if diff < 0 {
		result.AlertMessage = fmt.Sprintf(
			"ALERT: %s reserve deficit! diff=%d minor units (reserve=%d liability=%d pending_redemption=%d)",
			assetSymbol, diff,
			reserve.ConfirmedBalanceMinor, internalLiability, pendingRedemption,
		)
		fmt.Printf("%s\n", result.AlertMessage)
	}

	// Cache the latest result.
	lastResult = result

	return result, nil
}

// GetLastResult returns the most recent reconciliation result, or nil if none.
func (s *ReconciliationService) GetLastResult() *model.ReconciliationResult {
	return lastResult
}

// DailyReconciliation iterates over all active assets and reconciles each one.
// Designed to be invoked from a cron job (e.g. every 24h at midnight UTC).
//
// Returns all results; any individual asset failure is included in the slice
// and does not abort the overall run.
func (s *ReconciliationService) DailyReconciliation(ctx context.Context) ([]model.ReconciliationResult, error) {
	// For shadow-ledger MVP, the asset list is small and stable.
	// In production, query the assets table for active 'shadow-ledger' entries.
	assets := []string{"vUSDC"}

	var results []model.ReconciliationResult
	for _, asset := range assets {
		r, err := s.Reconcile(ctx, asset)
		if err != nil {
			// Log the error and continue with remaining assets.
			errResult := model.ReconciliationResult{
				AssetSymbol:  asset,
				IsBalanced:   false,
				ReconciledAt: time.Now().UTC(),
				AlertMessage: fmt.Sprintf("reconciliation failed: %v", err),
			}
			results = append(results, errResult)
			continue
		}
		results = append(results, *r)
	}
	return results, nil
}
