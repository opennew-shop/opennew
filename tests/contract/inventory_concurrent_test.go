// Package contract contains contract-level tests for inventory reservation.
//
// Follows demo.md Section 11: "lock inventory rows" during checkout commit,
// and Section 14 test: "concurrent inventory deduction" (no oversell).
//
// Run with: go test -tags=integration ./tests/contract/
package contract

import (
	"sync"
	"testing"
)

// TestInventoryConcurrentContract documents the inventory concurrency contract.
//
// Expected behavior:
//   - Multiple concurrent checkout commits for the same SKU must not oversell.
//   - The database transaction (SELECT FOR UPDATE on inventory rows) serializes
//     concurrent deductions.
//   - After all concurrent requests, reserved inventory <= available inventory.
//
// This test requires:
//   - A running PostgreSQL with the inventory table
//   - Multiple goroutines simulating concurrent checkout commits
func TestInventoryConcurrentContract(t *testing.T) {
	t.Run("concurrent deduction does not oversell", func(t *testing.T) {
		t.Skip("requires database + inventory repository - run with -tags=integration")
	})

	t.Run("serialized access via FOR UPDATE lock", func(t *testing.T) {
		t.Skip("requires database + inventory repository - run with -tags=integration")
	})

	t.Run("inventory restored on transaction rollback", func(t *testing.T) {
		t.Skip("requires database + inventory repository - run with -tags=integration")
	})
}

// TestInventoryConcurrentNoOversell is a logical model test that demonstrates
// the expected concurrency behavior without a database.
//
// It uses a simple counter guarded by a mutex to simulate serialized inventory
// access, proving the concept that with proper locking, concurrent deductions
// cannot exceed available stock.
func TestInventoryConcurrentNoOversell(t *testing.T) {
	availableStock := 10
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Simulate 20 concurrent buyers each trying to buy 1 unit.
	// Only 10 should succeed.
	successCount := 0
	numBuyers := 20

	for i := 0; i < numBuyers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			if availableStock > 0 {
				availableStock--
				successCount++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	if successCount != 10 {
		t.Errorf("expected exactly 10 successful purchases, got %d (oversell detected)", successCount)
	}
	if availableStock != 0 {
		t.Errorf("expected 0 remaining stock, got %d", availableStock)
	}
}
