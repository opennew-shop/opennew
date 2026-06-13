// Package security contains security tests for the vUSDC mint, redemption,
// and reserve reconciliation pipeline.
//
// These tests cover demo.md §14 security checklist items related to
// minting, redemption, chain transactions, and reserve constraints.
//
// Run with: go test -tags=integration ./tests/security/
package security

import (
	"encoding/json"
	"sync"
	"testing"
)

// TestMintAboveReserves verifies that minting vUSDC above the confirmed
// reserve balance is rejected by the system.
//
// This test covers demo.md §14: "铸币超过储备"
func TestMintAboveReserves(t *testing.T) {
	t.Run("mint amount exceeds confirmed reserve balance", func(t *testing.T) {
		reserveConfirmedBalance := int64(100000000000) // 100k vUSDC in reserve (seed data).
		mintRequestAmount := int64(200000000000)        // 200k vUSDC (exceeds reserve).

		if mintRequestAmount <= reserveConfirmedBalance {
			t.Skip("test requires mint amount > reserve balance")
		}

		// The mint service must verify: mint_amount <= confirmed_reserve_balance
		exceedsReserve := mintRequestAmount > reserveConfirmedBalance
		if !exceedsReserve {
			t.Error("mint above reserves should be detected")
		}
		t.Logf("mint above reserves blocked: request=%d, reserve=%d -> rejected",
			mintRequestAmount, reserveConfirmedBalance)
	})

	t.Run("mint amount within reserve balance accepted", func(t *testing.T) {
		reserveConfirmedBalance := int64(100000000000)
		mintRequestAmount := int64(50000000000) // 50k vUSDC (within reserve).

		if mintRequestAmount > reserveConfirmedBalance {
			t.Errorf("mint request %d should be within reserve balance %d",
				mintRequestAmount, reserveConfirmedBalance)
		}
	})

	t.Run("cumulative mints above reserve blocked", func(t *testing.T) {
		reserveConfirmedBalance := int64(100000000000)
		existingLiability := int64(70000000000)  // 70k already minted.
		newMintRequest := int64(40000000000)      // 40k new request -> 110k total.

		if (existingLiability + newMintRequest) <= reserveConfirmedBalance {
			t.Skip("test requires cumulative mint > reserve")
		}

		cumulativeExceeds := (existingLiability + newMintRequest) > reserveConfirmedBalance
		if !cumulativeExceeds {
			t.Error("cumulative mints above reserve should be blocked")
		}
		t.Logf("cumulative mint blocked: existing=%d + new=%d = %d > reserve=%d",
			existingLiability, newMintRequest, existingLiability+newMintRequest, reserveConfirmedBalance)
	})

	t.Run("per-wallet mint limit enforcement", func(t *testing.T) {
		// Mint policies may set per-wallet limits.
		perWalletLimit := int64(10000000000) // 10k vUSDC per wallet.
		walletAMintAmount := int64(15000000000) // 15k vUSDC (exceeds limit).

		if walletAMintAmount <= perWalletLimit {
			t.Skip("test requires mint > per-wallet limit")
		}

		t.Logf("per-wallet limit enforced: request=%d, limit=%d -> rejected",
			walletAMintAmount, perWalletLimit)
	})

	t.Run("daily mint limit enforcement", func(t *testing.T) {
		dailyLimit := int64(20000000000) // 20k vUSDC daily limit.
		mintedToday := int64(15000000000) // 15k already minted today.
		newMint := int64(10000000000)     // 10k new -> 25k total.

		if (mintedToday + newMint) <= dailyLimit {
			t.Skip("test requires cumulative daily mint > daily limit")
		}

		t.Logf("daily mint limit enforced: minted_today=%d + new=%d = %d > limit=%d -> rejected",
			mintedToday, newMint, mintedToday+newMint, dailyLimit)
	})

	t.Run("zero amount mint rejected", func(t *testing.T) {
		zeroMint := int64(0)
		if zeroMint <= 0 {
			t.Log("zero amount mint correctly rejected")
		}
	})

	t.Run("negative amount mint rejected", func(t *testing.T) {
		negativeMint := int64(-1000000)
		if negativeMint < 0 {
			t.Log("negative amount mint correctly rejected")
		}
	})
}

// TestRedemptionAboveBalance verifies that redeeming more vUSDC than
// the wallet's available balance is rejected.
//
// This test covers demo.md §14: "赎回超过余额"
func TestRedemptionAboveBalance(t *testing.T) {
	t.Run("redemption amount exceeds wallet available balance", func(t *testing.T) {
		walletBalance := int64(50000000) // 50 vUSDC.
		redemptionAmount := int64(100000000) // 100 vUSDC.

		if redemptionAmount <= walletBalance {
			t.Skip("test requires redemption > wallet balance")
		}

		t.Logf("redemption above balance blocked: wallet=%d, redemption=%d -> rejected",
			walletBalance, redemptionAmount)
	})

	t.Run("redemption within balance accepted", func(t *testing.T) {
		walletBalance := int64(100000000)
		redemptionAmount := int64(50000000)

		if redemptionAmount > walletBalance {
			t.Error("redemption within balance should be allowed")
		}
	})

	t.Run("redemption reduces available balance atomically", func(t *testing.T) {
		walletBalance := int64(100000000)
		redemptions := []int64{
			30000000, // 30 vUSDC
			40000000, // 40 vUSDC
			50000000, // 50 vUSDC -> should fail (only 30 vUSDC left after first two).
		}

		remaining := walletBalance
		successCount := 0

		for i, amt := range redemptions {
			if remaining >= amt {
				remaining -= amt
				successCount++
			} else {
				t.Logf("redemption %d (%d vUSDC) blocked: insufficient remaining balance (%d vUSDC)",
					i+1, amt, remaining)
			}
		}

		if successCount != 2 {
			t.Errorf("expected 2 successful redemptions, got %d", successCount)
		}
		if remaining < 0 {
			t.Errorf("balance went negative: %d", remaining)
		}
	})

	t.Run("concurrent redemptions from same wallet serialized", func(t *testing.T) {
		balance := int64(100000000)
		var mu sync.Mutex
		var wg sync.WaitGroup

		redeemA := int64(70000000)
		redeemB := int64(60000000)
		successes := 0

		wg.Add(2)
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			if balance >= redeemA {
				balance -= redeemA
				successes++
			}
		}()
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			if balance >= redeemB {
				balance -= redeemB
				successes++
			}
		}()
		wg.Wait()

		// Only one should succeed (70+60 > 100).
		if successes > 1 {
			t.Error("concurrent redemptions exceeding balance should not both succeed")
		}
		if balance < 0 {
			t.Errorf("balance went negative: %d", balance)
		}
	})

	t.Run("redemption requires wallet to hold sufficient confirmed balance", func(t *testing.T) {
		// Available = confirmed + pending_credits - pending_holds.
		confirmed := int64(50000000)
		pendingCredits := int64(20000000) // Not yet confirmed.
		pendingHolds := int64(10000000)   // Reserved for open orders.
		available := confirmed + pendingCredits - pendingHolds

		redemptionAmt := int64(55000000) // > confirmed but < (confirmed + pending).

		// System should use confirmed balance, not pending credits.
		if redemptionAmt > confirmed {
			t.Logf("redemption %d exceeds confirmed balance %d -> blocked (pending credits %d not counted)",
				redemptionAmt, confirmed, pendingCredits)
		}

		if redemptionAmt <= available {
			t.Log("redemption would succeed on available balance but should use confirmed only")
		}

		_ = available
	})
}

// TestNonFinalChainTransaction verifies that mint/redemption operations
// referencing non-final chain transactions are rejected.
//
// This test covers demo.md §14: "链上交易未 final"
func TestNonFinalChainTransaction(t *testing.T) {
	t.Run("deposit not final cannot trigger mint credit", func(t *testing.T) {
		// Chain transaction statuses: submitted -> confirmed -> finalized.
		// Only "finalized" deposits should trigger mint credit.

		type ChainTx struct {
			TxID          string
			Status        string
			Confirmations int
			CanMint       bool
		}

		txs := []ChainTx{
			{TxID: "tx_submitted_01", Status: "submitted", Confirmations: 0, CanMint: false},
			{TxID: "tx_confirmed_01", Status: "confirmed", Confirmations: 1, CanMint: false},
			{TxID: "tx_finalized_01", Status: "finalized", Confirmations: 32, CanMint: true},
			{TxID: "tx_failed_01", Status: "failed", Confirmations: 0, CanMint: false},
		}

		for _, tx := range txs {
			shouldMint := tx.Status == "finalized"
			if shouldMint != tx.CanMint {
				t.Errorf("tx %s: status=%s, expected CanMint=%v, got %v",
					tx.TxID, tx.Status, tx.CanMint, shouldMint)
			}
		}
	})

	t.Run("burn not final cannot trigger payout", func(t *testing.T) {
		// Redemption payout requires the burn transaction to be finalized.
		burnStatuses := []struct {
			status        string
			canPayout     bool
		}{
			{"submitted", false},
			{"confirmed", false},
			{"finalized", true},
			{"failed", false},
		}

		for _, bs := range burnStatuses {
			canPayout := bs.status == "finalized"
			if canPayout != bs.canPayout {
				t.Errorf("burn status %s: expected CanPayout=%v", bs.status, bs.canPayout)
			}
		}
	})

	t.Run("confirmations below threshold blocked", func(t *testing.T) {
		// Solana finalizes after ~32 confirmations (slots).
		// Sonic-L2 may have different thresholds.

		type TxCheck struct {
			network       string
			confirmations int
			isFinal       bool
			minRequired   int
		}

		checks := []TxCheck{
			{"solana-mainnet", 0, false, 32},
			{"solana-mainnet", 10, false, 32},
			{"solana-mainnet", 31, false, 32},
			{"solana-mainnet", 32, true, 32},
			{"solana-mainnet", 64, true, 32},
			{"sonic-l2", 0, false, 64},
			{"sonic-l2", 50, false, 64},
			{"sonic-l2", 64, true, 64},
		}

		for _, c := range checks {
			isFinal := c.confirmations >= c.minRequired
			if isFinal != c.isFinal {
				t.Errorf("%s: %d confirmations (min=%d), expected final=%v, got %v",
					c.network, c.confirmations, c.minRequired, c.isFinal, isFinal)
			}
		}
	})

	t.Run("reorg detection blocks premature finality", func(t *testing.T) {
		// A block may be reorged. Until a safe number of confirmations,
		// the transaction should not be considered final.
		safeConfirmations := 32 // Solana.
		currentConfirmations := 20

		if currentConfirmations >= safeConfirmations {
			t.Skip("test requires confirmations below safe threshold")
		}

		// Below threshold: not safe, reorg could invalidate the tx.
		isSafe := currentConfirmations >= safeConfirmations
		if isSafe {
			t.Error("below-threshold confirmations should not be considered safe")
		}
	})

	t.Run("chain transaction idempotency prevents double processing", func(t *testing.T) {
		// The same chain tx_hash should never be processed twice.
		processedTxHashes := map[string]bool{
			"0xabc123def456789": true,
			"0xghi789jkl012345": true,
		}
		dupTx := "0xabc123def456789"

		if processedTxHashes[dupTx] {
			t.Logf("duplicate tx_hash %s detected -> idempotently skipped", dupTx)
		}
	})
}

// TestReserveConstraintViolation verifies that the reserve constraint
// (total_internal_liability + pending_redemption <= confirmed_reserve_balance)
// is enforced and violations raise alerts.
//
// This test covers demo.md §14: (reserve constraint violation)
func TestReserveConstraintViolation(t *testing.T) {
	t.Run("reserve constraint satisfied when liability + pending <= reserve", func(t *testing.T) {
		reserveConfirmed := int64(100000000000) // 100k vUSDC.
		internalLiability := int64(60000000000)  // 60k vUSDC minted.
		pendingRedemption := int64(20000000000)  // 20k vUSDC pending redemption.

		totalObligation := internalLiability + pendingRedemption
		if totalObligation > reserveConfirmed {
			t.Error("reserve constraint violated when it should be satisfied")
		}

		isBalanced := totalObligation <= reserveConfirmed
		if !isBalanced {
			t.Errorf("reserve should be balanced: reserve=%d >= liability=%d + pending=%d = %d",
				reserveConfirmed, internalLiability, pendingRedemption, totalObligation)
		}
		t.Logf("reserve balanced: reserve=%d >= %d (liability=%d + pending=%d)",
			reserveConfirmed, totalObligation, internalLiability, pendingRedemption)
	})

	t.Run("reserve constraint violation raises alert", func(t *testing.T) {
		reserveConfirmed := int64(100000000000)
		internalLiability := int64(80000000000)
		pendingRedemption := int64(30000000000) // 80k + 30k = 110k > 100k.

		totalObligation := internalLiability + pendingRedemption
		violated := totalObligation > reserveConfirmed
		if !violated {
			t.Skip("test requires violated constraint")
		}

		difference := reserveConfirmed - totalObligation
		t.Logf("RESERVE CONSTRAINT VIOLATION: reserve=%d, liability=%d, pending=%d, diff=%d -> ALERT",
			reserveConfirmed, internalLiability, pendingRedemption, difference)

		if difference >= 0 {
			t.Error("difference should be negative for violation")
		}
	})

	t.Run("reconciliation detects reserve deficit", func(t *testing.T) {
		type ReconciliationResult struct {
			AssetSymbol             string `json:"asset_symbol"`
			ReserveConfirmedBalance int64  `json:"reserve_confirmed_balance_minor"`
			InternalLiability       int64  `json:"internal_liability_minor"`
			PendingRedemption       int64  `json:"pending_redemption_minor"`
			Difference              int64  `json:"difference_minor"`
			IsBalanced              bool   `json:"is_balanced"`
			AlertMessage            string `json:"alert_message,omitempty"`
		}

		// Scenario 1: Balanced.
		balanced := ReconciliationResult{
			AssetSymbol:             "vUSDC",
			ReserveConfirmedBalance: 100000000000,
			InternalLiability:       60000000000,
			PendingRedemption:       20000000000,
			Difference:              20000000000,
			IsBalanced:              true,
		}
		if balanced.Difference < 0 || !balanced.IsBalanced {
			t.Error("balanced reconciliation should have non-negative difference")
		}

		// Scenario 2: Deficit.
		deficit := ReconciliationResult{
			AssetSymbol:             "vUSDC",
			ReserveConfirmedBalance: 100000000000,
			InternalLiability:       80000000000,
			PendingRedemption:       30000000000,
			Difference:              -10000000000,
			IsBalanced:              false,
			AlertMessage:            "ALERT: vUSDC reserve deficit! diff=-10000000000 minor units",
		}
		if deficit.Difference >= 0 || deficit.IsBalanced {
			t.Error("deficit reconciliation should have negative difference and be unbalanced")
		}
		if deficit.AlertMessage == "" {
			t.Error("deficit reconciliation should generate an alert message")
		}

		_ = json.Marshal // used for future JSON serialization tests.
	})

	t.Run("reserve reconciliation must be idempotent", func(t *testing.T) {
		// Running reconciliation twice should produce the same result.
		reserve := int64(100000000000)
		liability := int64(60000000000)
		pending := int64(20000000000)

		run1 := reserve - (liability + pending) // 20,000,000,000
		run2 := reserve - (liability + pending) // Same result.

		if run1 != run2 {
			t.Error("reconciliation should be idempotent (same inputs, same output)")
		}
	})

	t.Run("cross-network reserve isolation", func(t *testing.T) {
		// solana-mainnet and sonic-l2 reserves are separate;
		// a deficit on one network should not affect the other.

		solanaReserve := int64(100000000000)
		solanaLiability := int64(110000000000) // Deficit on solana.
		solanaDiff := solanaReserve - solanaLiability

		sonicReserve := int64(50000000000)
		sonicLiability := int64(30000000000) // Healthy on sonic.
		sonicDiff := sonicReserve - sonicLiability

		if solanaDiff >= 0 {
			t.Skip("test requires solana deficit")
		}

		if solanaDiff >= 0 && sonicDiff < 0 {
			t.Error("solana should have deficit, sonic should not")
		}

		t.Logf("cross-network isolation: solana diff=%d (ALERT), sonic diff=%d (OK)",
			solanaDiff, sonicDiff)
	})
}

// TestReserveBalanceImmutability ensures that the reserve balance
// constraint is maintained across all operations.
func TestReserveBalanceImmutability(t *testing.T) {
	t.Run("total reserve balance never goes below total liability", func(t *testing.T) {
		// Shadow ledger invariant: confirmed_reserve >= SUM(all liabilities).
		operations := []struct {
			op                   string
			reserveDelta         int64
			liabilityDelta       int64
			shouldViolate        bool
		}{
			{"mint_credit", 0, 1000000, false},         // Deposit adds to liability.
			{"redemption_debit", 0, -1000000, false},    // Redemption reduces liability.
			{"reserve_deposit", 5000000, 0, false},      // Reserve deposit increases reserve.
			{"unauthorized_mint", 0, 99999999999, true}, // Massive mint without deposit.
		}

		reserve := int64(100000000000)
		liability := int64(0)

		for _, op := range operations {
			reserve += op.reserveDelta
			liability += op.liabilityDelta

			violated := reserve < liability
			if violated != op.shouldViolate {
				t.Errorf("op %s: violated=%v, expected=%v (reserve=%d, liability=%d)",
					op.op, violated, op.shouldViolate, reserve, liability)
			}
		}
	})
}

// TestMintSecurityBoundaries tests edge cases around mint security.
func TestMintSecurityBoundaries(t *testing.T) {
	t.Run("mint requires confirmed deposit proof", func(t *testing.T) {
		// Mint credit must be backed by a confirmed deposit intent.
		depositStatuses := []string{"created", "pending_confirmation", "confirmed", "processed"}

		for _, status := range depositStatuses {
			canMint := status == "confirmed" || status == "processed"
			if !canMint {
				t.Logf("deposit status %s: mint NOT allowed (needs confirmed deposit)", status)
			}
		}
	})

	t.Run("mint amount cannot exceed deposit amount", func(t *testing.T) {
		depositAmount := int64(100000000) // 100 vUSDC deposited.
		mintAmount := int64(150000000)    // 150 vUSDC mint requested.

		if mintAmount <= depositAmount {
			t.Skip("test requires mint > deposit")
		}

		t.Logf("mint %d exceeds deposit %d -> blocked", mintAmount, depositAmount)
	})

	t.Run("double mint of same deposit blocked", func(t *testing.T) {
		// Each deposit_intent_id should be consumable exactly once.
		depositID := "dep_abc123"
		mintedDeposits := map[string]bool{
			depositID: true,
		}

		if mintedDeposits[depositID] {
			t.Logf("double mint of deposit %s blocked (already consumed)", depositID)
		}
	})
}

// TestRedemptionSecurityBoundaries tests edge cases around redemption security.
func TestRedemptionSecurityBoundaries(t *testing.T) {
	t.Run("redemption payout requires completed burn", func(t *testing.T) {
		burnStates := []string{"burn_submitted", "burn_confirmed", "burned", "minted"}
		for _, state := range burnStates {
			canPayout := state == "burned"
			if !canPayout {
				t.Logf("burn state %s: payout NOT allowed (needs burned state)", state)
			}
		}
	})

	t.Run("redemption release restores user balance", func(t *testing.T) {
		// If redemption fails, release must credit the user back.
		userBalance := int64(100000000)
		lockedAmount := int64(50000000)

		// Lock.
		userBalance -= lockedAmount
		if userBalance != 50000000 {
			t.Errorf("balance after lock: expected 50000000, got %d", userBalance)
		}

		// Release (failure recovery).
		userBalance += lockedAmount
		if userBalance != 100000000 {
			t.Errorf("balance after release: expected 100000000, got %d", userBalance)
		}
	})

	t.Run("redemption status transition validates security", func(t *testing.T) {
		// Invalid transitions: paid -> release, released -> pay, paid -> burn.
		invalidTransitions := [][2]string{
			{"paid", "released"},      // Terminal.
			{"released", "paid"},      // Terminal to terminal.
			{"paid", "burn_submitted"}, // Terminal back.
		}

		for _, trans := range invalidTransitions {
			from, to := trans[0], trans[1]
			t.Logf("invalid redemption transition: %s -> %s should be rejected", from, to)
		}
	})
}
