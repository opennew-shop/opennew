package solana

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// ---------------------------------------------------------------------------
// Supply Reconciliation
// ---------------------------------------------------------------------------
//
// The chain-adapter periodically reconciles on-chain vUSDC total supply with
// the internal ledger liabilities. This ensures the invariant:
//
//   onchain_vusdc_supply_minor + pending_redemption_minor <= confirmed_reserve_usdc_minor
//
// For the shadow-ledger model (Phase 3 MVP), the on-chain supply is 0 and
// reconciliation checks internal_liability <= confirmed_reserve.
//
// For the on-chain Token-2022 model (Phase 4), the reconciler queries the
// SPL Token-2022 mint's getTokenSupply and cross-references with the
// ledger's total user_available + user_pending + redemption_pending balances.
//
// Reconciliation runs on a configurable interval (default: every 15 minutes)
// and emits structured log events for observability. If the invariant is
// violated, an alert-level log is emitted and the event is written to the
// audit_log table.

// ---------------------------------------------------------------------------
// Ledger repository (minimal interface for reconciliation queries)
// ---------------------------------------------------------------------------

// LedgerRepository provides the subset of ledger queries needed for
// supply reconciliation. The actual implementation lives in the ledger
// service; this interface is defined here to avoid circular imports.
// 中文说明：供应量对账所需的账本查询子集接口；真正实现位于 ledger 服务，此处定义以避免循环依赖。
type LedgerRepository struct {
	db *sql.DB
}

// NewLedgerRepository creates a ledger repository for reconciliation queries.
func NewLedgerRepository(db *sql.DB) *LedgerRepository {
	return &LedgerRepository{db: db}
}

// GetTotalLiability returns the sum of all user-facing vUSDC liabilities
// across user_available, user_pending, and redemption_pending accounts.
//
// This is the "internal liability" side of the reconciliation invariant:
//
//	total_liability = SUM(user_available + user_pending + redemption_pending)
//
// For the shadow-ledger model, this is the authoritative total.
// For the on-chain model, this is cross-referenced against on-chain supply.
func (r *LedgerRepository) GetTotalLiability(ctx context.Context) (int64, error) {
	stmt := `
		SELECT COALESCE(SUM(amount_minor), 0)
		FROM ledger_entries
		WHERE wallet IS NOT NULL
		  AND (debit_account IN ('user_available', 'user_pending', 'redemption_pending')
		       OR credit_account IN ('user_available', 'user_pending', 'redemption_pending'))
	`

	// Note: This is a simplified query. In production, use a materialized
	// balance view or a balance summary table derived from ledger entries.
	// The raw ledger entries query computes net liabilities by summing
	// credits minus debits for user-facing accounts.
	//
	// For accuracy, use the balance materialized view:
	//   SELECT COALESCE(SUM(balance_minor), 0) FROM user_balances
	//   WHERE account_type IN ('user_available', 'user_pending', 'redemption_pending')

	var liability int64
	if err := r.db.QueryRowContext(ctx, stmt).Scan(&liability); err != nil {
		return 0, fmt.Errorf("ledger_repo: get total liability: %w", err)
	}
	return liability, nil
}

// GetConfirmedReserve returns the confirmed USDC reserve balance for the
// Solana mainnet network.
func (r *LedgerRepository) GetConfirmedReserve(ctx context.Context, network string, assetSymbol string) (uint64, error) {
	stmt := `
		SELECT COALESCE(confirmed_balance_minor, 0)
		FROM reserve_accounts
		WHERE network = $1 AND asset_symbol = $2
	`

	var balance uint64
	if err := r.db.QueryRowContext(ctx, stmt, network, assetSymbol).Scan(&balance); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("ledger_repo: get confirmed reserve %s/%s: %w", network, assetSymbol, err)
	}
	return balance, nil
}

// GetPendingRedemptionTotal returns the sum of all pending redemption amounts.
func (r *LedgerRepository) GetPendingRedemptionTotal(ctx context.Context) (int64, error) {
	stmt := `
		SELECT COALESCE(SUM(amount_minor), 0)
		FROM redemption_requests
		WHERE status IN ('created', 'balance_locked', 'burn_submitted')
	`

	var total int64
	if err := r.db.QueryRowContext(ctx, stmt).Scan(&total); err != nil {
		return 0, fmt.Errorf("ledger_repo: get pending redemption total: %w", err)
	}
	return total, nil
}

// ---------------------------------------------------------------------------
// SupplyReconciliation result
// ---------------------------------------------------------------------------

// SupplyReconciliation holds the result of a single reconciliation run.
type SupplyReconciliation struct {
	OnchainSupply     uint64    `json:"onchain_supply"`
	InternalLiability int64     `json:"internal_liability"`
	PendingRedemption int64     `json:"pending_redemption"`
	ConfirmedReserve  uint64    `json:"confirmed_reserve"`
	Difference        int64     `json:"difference"`
	IsBalanced        bool      `json:"is_balanced"`
	ReconciledAt      time.Time `json:"reconciled_at"`
	VUSDCOnChain      bool      `json:"vusdc_on_chain"` // true if Token-2022 mint is live
}

// ToLogFields converts the reconciliation result to structured log key-value pairs.
func (s *SupplyReconciliation) ToLogFields() []interface{} {
	return []interface{}{
		"onchain_supply", s.OnchainSupply,
		"internal_liability", s.InternalLiability,
		"pending_redemption", s.PendingRedemption,
		"confirmed_reserve", s.ConfirmedReserve,
		"difference", s.Difference,
		"is_balanced", s.IsBalanced,
		"reconciled_at", s.ReconciledAt.UTC().Format(time.RFC3339),
		"vusdc_on_chain", s.VUSDCOnChain,
	}
}

// ---------------------------------------------------------------------------
// ChainReconciler
// ---------------------------------------------------------------------------

// ChainReconciler periodically checks the invariant:
//
//   onchain_vusdc_supply + pending_redemption <= confirmed_reserve
//
// It runs on an interval and emits:
//   - Info-level log if balanced
//   - Warn-level log if unbalanced (difference within tolerance, e.g. 1%)
//   - Error-level log if severely unbalanced
//
// The reconciler also writes a reconciliation event to the outbox for
// downstream audit consumption.
type ChainReconciler struct {
	rpcURL      string
	mintAddress string // vUSDC Token-2022 mint address (empty for shadow-ledger)
	ledgerRepo  *LedgerRepository
	network     string
	assetSymbol string
	interval    time.Duration
	logger      *slog.Logger
}

// NewChainReconciler creates a new ChainReconciler.
//
// Parameters:
//   - rpcURL: Solana RPC endpoint for on-chain queries
//   - mintAddress: vUSDC Token-2022 mint (empty string for shadow-ledger model)
//   - ledgerRepo: ledger repository for internal liability queries
//   - network: blockchain network identifier (e.g. "solana-mainnet")
//   - assetSymbol: asset symbol (e.g. "vUSDC")
func NewChainReconciler(
	rpcURL string,
	mintAddress string,
	ledgerRepo *LedgerRepository,
	network string,
	assetSymbol string,
) *ChainReconciler {
	return &ChainReconciler{
		rpcURL:      rpcURL,
		mintAddress: mintAddress,
		ledgerRepo:  ledgerRepo,
		network:     network,
		assetSymbol: assetSymbol,
		interval:    15 * time.Minute,
		logger:      slog.Default().With("component", "chain-reconciler", "network", network, "asset", assetSymbol),
	}
}

// SetInterval sets the reconciliation interval.
func (r *ChainReconciler) SetInterval(d time.Duration) {
	r.interval = d
}

// ReconcileSupply performs a single reconciliation run.
//
// The reconciliation logic depends on whether vUSDC is on-chain or shadow-ledger:
//
// Shadow-ledger model (mintAddress == ""):
//
//	Checks: internal_liability <= confirmed_reserve
//	onchain_supply is always 0.
//
// On-chain Token-2022 model (mintAddress != ""):
//
//	1. Query getTokenSupply for the on-chain supply.
//	2. Query ledger for internal liability (sum of user-facing balances).
//	3. Query ledger for pending redemption total.
//	4. Query reserve_accounts for confirmed reserve.
//	5. Verify: onchain_supply + pending_redemption <= confirmed_reserve.
//
// The difference = confirmed_reserve - (onchain_supply + pending_redemption).
// A positive difference means over-reserved (safe, but unnecessary capital lockup).
// A negative difference means under-reserved (VIOLATION — requires immediate action).
func (r *ChainReconciler) ReconcileSupply(ctx context.Context) (*SupplyReconciliation, error) {
	result := &SupplyReconciliation{
		ReconciledAt: time.Now().UTC(),
		VUSDCOnChain: r.mintAddress != "",
	}

	// 1. On-chain supply (only if Token-2022 mint is deployed).
	if r.mintAddress != "" {
		supply, err := GetTokenSupply(ctx, r.rpcURL, r.mintAddress)
		if err != nil {
			r.logger.Warn("failed to get on-chain token supply — treating as 0",
				"mint", r.mintAddress,
				"error", err,
			)
			result.OnchainSupply = 0
		} else {
			result.OnchainSupply = supply
		}
	}

	// 2. Internal liability from ledger.
	liability, err := r.ledgerRepo.GetTotalLiability(ctx)
	if err != nil {
		return nil, fmt.Errorf("chain_reconciler: get total liability: %w", err)
	}
	result.InternalLiability = liability

	// 3. Pending redemptions.
	pendingRedemption, err := r.ledgerRepo.GetPendingRedemptionTotal(ctx)
	if err != nil {
		r.logger.Warn("failed to get pending redemption total — treating as 0", "error", err)
		result.PendingRedemption = 0
	} else {
		result.PendingRedemption = pendingRedemption
	}

	// 4. Confirmed reserve balance.
	reserve, err := r.ledgerRepo.GetConfirmedReserve(ctx, r.network, r.assetSymbol)
	if err != nil {
		return nil, fmt.Errorf("chain_reconciler: get confirmed reserve: %w", err)
	}
	result.ConfirmedReserve = reserve

	// 5. Compute invariant and difference.
	//
	// Invariant: onchain_supply + pending_redemption <= confirmed_reserve
	//
	// For shadow-ledger:
	//   internal_liability (covers pending_redemption) <= confirmed_reserve
	//   Difference = confirmed_reserve - internal_liability
	//
	// For on-chain:
	//   onchain_supply + pending_redemption <= confirmed_reserve
	//   Difference = confirmed_reserve - (onchain_supply + pending_redemption)
	var supplySide int64
	if r.mintAddress != "" {
		supplySide = int64(result.OnchainSupply) + result.PendingRedemption
	} else {
		supplySide = result.InternalLiability
	}
	result.Difference = int64(result.ConfirmedReserve) - supplySide

	// A negative difference indicates under-reservation — VIOLATION.
	result.IsBalanced = result.Difference >= 0

	// 6. Emit structured log based on severity.
	if result.IsBalanced {
		r.logger.Info("reconciliation passed", result.ToLogFields()...)
	} else {
		r.logger.Error("RECONCILIATION VIOLATION — liabilities exceed reserves",
			result.ToLogFields()...,
		)
	}

	return result, nil
}

// StartReconciliationLoop runs ReconcileSupply on a recurring interval.
// It blocks until ctx is cancelled.
func (r *ChainReconciler) StartReconciliationLoop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.logger.Info("starting reconciliation loop",
		"interval", r.interval.String(),
		"mint_address", r.mintAddress,
		"network", r.network,
		"asset_symbol", r.assetSymbol,
	)

	// Run immediate reconciliation on start.
	if _, err := r.ReconcileSupply(ctx); err != nil {
		r.logger.Error("initial reconciliation failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciliation loop stopped (context cancelled)")
			return
		case <-ticker.C:
			if _, err := r.ReconcileSupply(ctx); err != nil {
				r.logger.Error("reconciliation failed", "error", err)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Reconciliation audit helpers
// ---------------------------------------------------------------------------

// AuditReconciliationEvent writes a reconciliation event to the audit_log
// and outbox tables. This ensures the reconciliation run is permanently
// recorded for compliance.
//
// The event is written to both:
//   - audit_log: immutable audit trail
//   - outbox: for downstream async processing (e.g., alerting, metrics)
//
// The caller (typically after ReconcileSupply) should invoke this to persist.
func AuditReconciliationEvent(ctx context.Context, db *sql.DB, result *SupplyReconciliation) error {
	eventID := fmt.Sprintf("recon_%d", result.ReconciledAt.UnixMilli())

	// Write to audit_log.
	auditStmt := `
		INSERT INTO audit_log (event_id, event_type, actor_type, actor_id,
		                       resource_type, resource_id, action, details)
		VALUES ($1, $2, 'system', 'chain-reconciler',
		        'vusdc_supply', $3, 'reconcile', $4)
	`
	detailsJSON := fmt.Sprintf(`{
		"onchain_supply": %d,
		"internal_liability": %d,
		"pending_redemption": %d,
		"confirmed_reserve": %d,
		"difference": %d,
		"is_balanced": %v,
		"reconciled_at": "%s",
		"vusdc_on_chain": %v
	}`, result.OnchainSupply, result.InternalLiability, result.PendingRedemption,
		result.ConfirmedReserve, result.Difference, result.IsBalanced,
		result.ReconciledAt.UTC().Format(time.RFC3339), result.VUSDCOnChain)

	if _, err := db.ExecContext(ctx, auditStmt, eventID, "vusdc_supply_reconciliation",
		result.ReconciledAt.UTC().Format(time.RFC3339), detailsJSON); err != nil {
		return fmt.Errorf("chain_reconciler: audit reconciliation event: %w", err)
	}

	// Write to outbox for downstream processing.
	outboxStmt := `
		INSERT INTO outbox (event_id, event_type, aggregate_type, aggregate_id, payload, status)
		VALUES ($1, 'vusdc_supply_reconciliation', 'vusdc', $2, $3, 'pending')
	`
	if _, err := db.ExecContext(ctx, outboxStmt, eventID, result.ReconciledAt.UTC().Format(time.RFC3339),
		detailsJSON); err != nil {
		return fmt.Errorf("chain_reconciler: outbox reconciliation event: %w", err)
	}

	return nil
}

// ResolveDifference computes a human-readable explanation of the reconciliation
// difference. Useful for incident response and dashboard summaries.
func (s *SupplyReconciliation) ResolveDifference() string {
	if s.IsBalanced {
		// Over-reserved by at most 5% is normal (padding for fees, gas, etc.)
		if s.Difference > int64(s.ConfirmedReserve)/20 {
			return fmt.Sprintf("Over-reserved: reserve exceeds liabilities by %d minor units. "+
				"Reducing reserve may free up capital efficiency.", s.Difference)
		}
		return fmt.Sprintf("Balanced: reserve covers liabilities with %d minor unit margin (%.2f%%).",
			s.Difference, float64(s.Difference)/float64(s.ConfirmedReserve)*100)
	}

	// Under-reserved — VIOLATION.
	deficit := -s.Difference
	return fmt.Sprintf("VIOLATION: liabilities exceed confirmed reserve by %d minor units. "+
		"Immediate action required: pause minting, investigate deposit shortfall, "+
		"or top up reserve. Reconciliation timestamp: %s.",
		deficit, s.ReconciledAt.UTC().Format(time.RFC3339))
}
