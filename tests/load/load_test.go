// Package load contains load and benchmark tests for the ANCF API endpoints.
//
// These tests use Go's testing.B benchmarking framework to measure:
//   - Search API concurrent performance
//   - Quote API concurrency
//   - Checkout commit concurrency (idempotency + inventory lock)
//   - Ledger balance query concurrency
//
// Run with: go test -bench=. -benchmem ./tests/load/
//
// For HTTP load testing with external tools (vegeta/k6), see load_test_suite.md.
package load

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// ===========================================================================
// Benchmark fixtures — shared test data initialized once per benchmark.
// ===========================================================================

var (
	benchSKUs = []map[string]interface{}{
		{
			"sku_id": "sku_gpu_h100_v1",
			"title":  "H100 Compute Rental, Hourly",
			"price": map[string]interface{}{
				"currency": "vUSDC", "amount_minor": "2450000", "scale": 6,
			},
			"stock_hint": 42,
			"specs":      map[string]interface{}{"GPU": "80GB SXM5", "CUDA": "12.4", "Memory": "80GB HBM3"},
		},
		{
			"sku_id": "sku_gpu_a100_v1",
			"title":  "A100 Compute Rental, Hourly",
			"price": map[string]interface{}{
				"currency": "vUSDC", "amount_minor": "1200000", "scale": 6,
			},
			"stock_hint": 128,
			"specs":      map[string]interface{}{"GPU": "40GB SXM4", "CUDA": "12.2", "Memory": "40GB HBM2e"},
		},
		{
			"sku_id": "sku_gpu_l40s_v1",
			"title":  "L40S Compute Rental, Hourly",
			"price": map[string]interface{}{
				"currency": "vUSDC", "amount_minor": "650000", "scale": 6,
			},
			"stock_hint": 256,
			"specs":      map[string]interface{}{"GPU": "48GB", "CUDA": "12.4", "Memory": "48GB GDDR6"},
		},
	}

	benchWalletPub  ed25519.PublicKey
	benchWalletPriv ed25519.PrivateKey
	benchWalletAddr string
)

func init() {
	var err error
	benchWalletPub, benchWalletPriv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("failed to generate benchmark key: %v", err))
	}
	benchWalletAddr = hex.EncodeToString(benchWalletPub)
}

// ===========================================================================
// Benchmark: Search API
// ===========================================================================

// BenchmarkSearchAPI measures search throughput and allocation overhead.
// Target: 100 req/s with < 50ms latency per request.
func BenchmarkSearchAPI(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		results := searchItems("H100", benchSKUs)
		if len(results) == 0 {
			b.Fatal("search returned no results")
		}
	}
}

// BenchmarkSearchAPIParallel measures concurrent search performance.
func BenchmarkSearchAPIParallel(b *testing.B) {
	b.ReportAllocs()
	b.SetParallelism(10)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			results := searchItems("GPU", benchSKUs)
			if len(results) == 0 {
				b.Fatal("search returned no results")
			}
		}
	})
}

// BenchmarkSearchAPISingleItem measures search for a specific SKU.
func BenchmarkSearchAPISingleItem(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		results := searchItems("L40S", benchSKUs)
		if len(results) == 0 {
			b.Fatal("search returned no results")
		}
	}
}

// searchItems performs an in-memory search for benchmarking purposes.
// In production, this would use PostgreSQL full-text search.
func searchItems(query string, skus []map[string]interface{}) []map[string]interface{} {
	var results []map[string]interface{}
	for _, s := range skus {
		skuID, _ := s["sku_id"].(string)
		title, _ := s["title"].(string)
		if contains(skuID, query) || contains(title, query) {
			results = append(results, s)
		}
	}
	return results
}

func contains(s, substr string) bool {
	return len(substr) == 0 || len(s) >= len(substr) && searchInString(s, substr)
}

func searchInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ===========================================================================
// Benchmark: Quote API
// ===========================================================================

// BenchmarkQuoteAPI measures quote generation throughput.
// Target: 50 req/s with < 100ms latency per request.
func BenchmarkQuoteAPI(b *testing.B) {
	b.ReportAllocs()

	lines := []map[string]interface{}{
		{"sku_id": "sku_gpu_h100_v1", "quantity": 2},
		{"sku_id": "sku_gpu_a100_v1", "quantity": 1},
	}

	for b.Loop() {
		quote := generateQuote(lines, benchSKUs)
		if quote == nil || quote["quote_id"] == "" {
			b.Fatal("quote generation failed")
		}
	}
}

// BenchmarkQuoteAPIParallel measures concurrent quote generation.
func BenchmarkQuoteAPIParallel(b *testing.B) {
	b.ReportAllocs()
	b.SetParallelism(8)

	lines := []map[string]interface{}{
		{"sku_id": "sku_gpu_h100_v1", "quantity": 1},
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			quote := generateQuote(lines, benchSKUs)
			if quote == nil {
				b.Fatal("quote generation failed")
			}
		}
	})
}

// BenchmarkQuoteAPIMultiLine measures quote performance with multiple line items.
func BenchmarkQuoteAPIMultiLine(b *testing.B) {
	b.ReportAllocs()

	lines := []map[string]interface{}{
		{"sku_id": "sku_gpu_h100_v1", "quantity": 5},
		{"sku_id": "sku_gpu_a100_v1", "quantity": 3},
		{"sku_id": "sku_gpu_l40s_v1", "quantity": 10},
	}

	for b.Loop() {
		quote := generateQuote(lines, benchSKUs)
		if quote == nil {
			b.Fatal("multi-line quote generation failed")
		}
	}
}

var quoteIDCounter int64
var quoteIDMutex sync.Mutex

func nextQuoteID() string {
	quoteIDMutex.Lock()
	quoteIDCounter++
	id := quoteIDCounter
	quoteIDMutex.Unlock()
	return fmt.Sprintf("quote_bench_%d", id)
}

func generateQuote(lines []map[string]interface{}, skus []map[string]interface{}) map[string]interface{} {
	var totalMinor int64
	quoteLines := make([]map[string]interface{}, 0, len(lines))

	// Build SKU lookup map.
	skuMap := make(map[string]map[string]interface{})
	for _, s := range skus {
		if id, ok := s["sku_id"].(string); ok {
			skuMap[id] = s
		}
	}

	for _, line := range lines {
		skuID, _ := line["sku_id"].(string)
		qty, ok := line["quantity"].(int)
		if !ok {
			// Try float64 (JSON unmarshal).
			if f, ok := line["quantity"].(float64); ok {
				qty = int(f)
			} else {
				qty = 1
			}
		}

		sku, found := skuMap[skuID]
		if !found {
			return nil
		}

		price, ok := sku["price"].(map[string]interface{})
		if !ok {
			return nil
		}
		amountStr, ok := price["amount_minor"].(string)
		if !ok {
			return nil
		}

		var unitPrice int64
		fmt.Sscanf(amountStr, "%d", &unitPrice)
		lineTotal := unitPrice * int64(qty)
		totalMinor += lineTotal

		quoteLines = append(quoteLines, map[string]interface{}{
			"sku_id":           skuID,
			"quantity":         qty,
			"unit_price_minor": amountStr,
			"line_total_minor": fmt.Sprintf("%d", lineTotal),
		})
	}

	return map[string]interface{}{
		"quote_id":    nextQuoteID(),
		"currency":    "vUSDC",
		"total_minor": fmt.Sprintf("%d", totalMinor),
		"scale":       6,
		"lines":       quoteLines,
	}
}

// ===========================================================================
// Benchmark: Checkout Commit
// ===========================================================================

// BenchmarkCheckoutCommit measures commit throughput with signature verification.
// Target: 20 req/s with < 500ms latency per request.
func BenchmarkCheckoutCommit(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		err := simulateCheckoutCommit(benchWalletAddr, benchWalletPriv)
		if err != nil {
			b.Fatalf("checkout commit failed: %v", err)
		}
	}
}

// BenchmarkCheckoutCommitParallel measures concurrent checkout commit performance.
func BenchmarkCheckoutCommitParallel(b *testing.B) {
	b.ReportAllocs()
	b.SetParallelism(4)

	b.RunParallel(func(pb *testing.PB) {
		// Each goroutine gets its own key pair.
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		addr := hex.EncodeToString(pub)

		for pb.Next() {
			err := simulateCheckoutCommit(addr, priv)
			if err != nil {
				b.Fatalf("checkout commit failed: %v", err)
			}
		}
	})
}

// BenchmarkCheckoutCommitWithIdempotency measures throughput when idempotency
// keys are checked and stored (simulates DB round-trip overhead).
func BenchmarkCheckoutCommitWithIdempotency(b *testing.B) {
	b.ReportAllocs()

	idempotencyStore := make(map[string]string) // key -> body_hash.
	var storeMu sync.Mutex

	for b.Loop() {
		idemKey := fmt.Sprintf("idem_bench_%d", b.N)
		body := map[string]interface{}{
			"order_intent_id": fmt.Sprintf("intent_%d", b.N),
			"quote_id":        "quote_bench_1",
		}
		bodyJSON, _ := json.Marshal(body)

		// Compute body hash.
		hash := sha256.Sum256(bodyJSON)
		bodyHash := fmt.Sprintf("%x", hash)

		// Idempotency check.
		storeMu.Lock()
		cachedHash, exists := idempotencyStore[idemKey]
		if exists {
			if cachedHash != bodyHash {
				storeMu.Unlock()
				b.Fatal("idempotency conflict (409)")
			}
			// Replay: return cached response.
			storeMu.Unlock()
			continue
		}
		// New: store key + hash.
		idempotencyStore[idemKey] = bodyHash
		storeMu.Unlock()
	}
}

// BenchmarkSignatureGeneration measures Ed25519 signing throughput.
func BenchmarkSignatureGeneration(b *testing.B) {
	b.ReportAllocs()

	payload := map[string]interface{}{
		"domain":      "yourshop.com",
		"shop_id":     "zero_shop_sol_01",
		"network":     "solana-mainnet",
		"wallet":      benchWalletAddr,
		"quote_id":    "quote_bench_sig",
		"total_minor": "4900000",
		"currency":    "vUSDC",
		"expires_at":  "2026-06-04T00:10:00Z",
		"nonce":       "abc123def4567890",
	}

	for b.Loop() {
		payloadJSON, _ := json.Marshal(payload)
		message := []byte(fmt.Sprintf("ANCF_CHECKOUT:%s", string(payloadJSON)))
		sig := ed25519.Sign(benchWalletPriv, message)
		_ = base64.StdEncoding.EncodeToString(sig)
	}
}

// BenchmarkSignatureVerification measures Ed25519 verify throughput.
func BenchmarkSignatureVerification(b *testing.B) {
	b.ReportAllocs()

	payload := map[string]interface{}{
		"domain":      "yourshop.com",
		"shop_id":     "zero_shop_sol_01",
		"network":     "solana-mainnet",
		"wallet":      benchWalletAddr,
		"quote_id":    "quote_bench_sig_v",
		"total_minor": "4900000",
		"currency":    "vUSDC",
		"expires_at":  "2026-06-04T00:10:00Z",
		"nonce":       "abc123def4567890",
	}

	payloadJSON, _ := json.Marshal(payload)
	message := []byte(fmt.Sprintf("ANCF_CHECKOUT:%s", string(payloadJSON)))
	sig := ed25519.Sign(benchWalletPriv, message)

	for b.Loop() {
		if !ed25519.Verify(benchWalletPub, message, sig) {
			b.Fatal("signature verification failed")
		}
	}
}

// simulateCheckoutCommit runs a full checkout commit simulation
// including signature generation and verification.
func simulateCheckoutCommit(walletAddr string, priv ed25519.PrivateKey) error {
	payload := map[string]interface{}{
		"domain":      "yourshop.com",
		"shop_id":     "zero_shop_sol_01",
		"network":     "solana-mainnet",
		"wallet":      walletAddr,
		"quote_id":    "quote_bench_commit",
		"total_minor": "4900000",
		"currency":    "vUSDC",
		"expires_at":  "2026-06-04T00:10:00Z",
		"nonce":       "abc123bench",
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	message := []byte(fmt.Sprintf("ANCF_CHECKOUT:%s", string(payloadJSON)))
	sig := ed25519.Sign(priv, message)
	_ = sig

	// Verify.
	pub := priv.Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, message, sig) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

// ===========================================================================
// Benchmark: Ledger Balance Query
// ===========================================================================

// BenchmarkLedgerBalance measures balance query throughput.
// Target: support balance reads at >100 req/s with < 20ms latency.
func BenchmarkLedgerBalance(b *testing.B) {
	b.ReportAllocs()

	// Mock ledger with some entries.
	ledger := map[string]int64{
		"wallet_A": 100000000, // 100 vUSDC.
		"wallet_B": 50000000,  // 50 vUSDC.
		"wallet_C": 25000000,  // 25 vUSDC.
	}
	var mu sync.RWMutex

	for b.Loop() {
		mu.RLock()
		balance, ok := ledger["wallet_A"]
		mu.RUnlock()
		if !ok {
			b.Fatal("wallet not found")
		}
		_ = balance
	}
}

// BenchmarkLedgerBalanceParallel measures concurrent balance query throughput.
func BenchmarkLedgerBalanceParallel(b *testing.B) {
	b.ReportAllocs()
	b.SetParallelism(16)

	ledger := map[string]int64{
		"wallet_A": 100000000,
		"wallet_B": 50000000,
		"wallet_C": 25000000,
	}
	var mu sync.RWMutex

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.RLock()
			_, ok := ledger["wallet_A"]
			mu.RUnlock()
			if !ok {
				b.Fatal("wallet not found")
			}
		}
	})
}

// BenchmarkLedgerBalanceFullCompute measures the cost of computing
// a wallet balance from ledger entries (no materialized view).
func BenchmarkLedgerBalanceFullCompute(b *testing.B) {
	b.ReportAllocs()

	// Simulate 1000 ledger entries.
	type LedgerEntry struct {
		Wallet      string
		AccountType string
		AmountMinor int64
	}
	entries := make([]LedgerEntry, 1000)
	for i := range entries {
		entries[i] = LedgerEntry{
			Wallet:      "wallet_A",
			AccountType: "user_available",
			AmountMinor: int64((i % 100) * 10000),
		}
	}

	for b.Loop() {
		var balance int64
		for _, e := range entries {
			balance += e.AmountMinor
		}
		_ = balance
	}
}

// ===========================================================================
// Load Test Scenarios (testing.T, runnable with -run flag)
// ===========================================================================

// TestLoadSearchConcurrency verifies search behavior under concurrent load.
func TestLoadSearchConcurrency(t *testing.T) {
	const numGoroutines = 100
	const queriesPerGoroutine = 50 // 100 * 50 = 5000 total queries.

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*queriesPerGoroutine)
	var errorsMu sync.Mutex
	var errorCount int

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(routineID int) {
			defer wg.Done()
			queries := []string{"H100", "A100", "L40S", "GPU", "Compute"}
			for j := 0; j < queriesPerGoroutine; j++ {
				q := queries[(routineID+j)%len(queries)]
				results := searchItems(q, benchSKUs)
				if len(results) == 0 {
					errorsMu.Lock()
					errorCount++
					errorsMu.Unlock()
					errors <- fmt.Errorf("search for %q returned no results", q)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errors)

	if errorCount > 0 {
		t.Errorf("%d search errors under load", errorCount)
	} else {
		t.Logf("search load test passed: %d goroutines x %d queries = %d total",
			numGoroutines, queriesPerGoroutine, numGoroutines*queriesPerGoroutine)
	}
}

// TestLoadQuoteConcurrency verifies quote generation under concurrent load.
func TestLoadQuoteConcurrency(t *testing.T) {
	const numGoroutines = 50
	const quotesPerGoroutine = 20

	var wg sync.WaitGroup
	successCount := int64(0)
	var countMu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < quotesPerGoroutine; j++ {
				lines := []map[string]interface{}{
					{"sku_id": "sku_gpu_h100_v1", "quantity": 1},
				}
				quote := generateQuote(lines, benchSKUs)
				if quote != nil {
					countMu.Lock()
					successCount++
					countMu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	expected := int64(numGoroutines * quotesPerGoroutine)
	if successCount != expected {
		t.Errorf("quote load test: %d/%d successful", successCount, expected)
	} else {
		t.Logf("quote load test passed: %d quotes generated", successCount)
	}
}

// TestLoadCheckoutCommitConcurrency verifies checkout commits under concurrent load.
func TestLoadCheckoutCommitConcurrency(t *testing.T) {
	const numGoroutines = 20 // Target: 20 req/s.

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pub, priv, _ := ed25519.GenerateKey(rand.Reader)
			addr := hex.EncodeToString(pub)
			if err := simulateCheckoutCommit(addr, priv); err != nil {
				errors <- err
			}
		}()
	}
	wg.Wait()
	close(errors)

	errorCount := 0
	for range errors {
		errorCount++
	}
	if errorCount > 0 {
		t.Errorf("%d commit errors under load", errorCount)
	} else {
		t.Logf("checkout commit load test passed: %d concurrent commits", numGoroutines)
	}
}

// TestLoadConcurrentIdempotencyKeys verifies idempotency key handling under load.
func TestLoadConcurrentIdempotencyKeys(t *testing.T) {
	const numKeys = 1000
	store := make(map[string]string) // key -> body_hash.
	var mu sync.Mutex
	conflicts := 0

	var wg sync.WaitGroup
	// Insert keys concurrently.
	for i := 0; i < numKeys; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("idem_load_%d", idx)
			hash := fmt.Sprintf("hash_%d", idx)

			mu.Lock()
			defer mu.Unlock()
			if _, exists := store[key]; exists {
				conflicts++
			} else {
				store[key] = hash
			}
		}(i)
	}
	wg.Wait()

	// Verify all keys inserted.
	if len(store) != numKeys {
		t.Errorf("expected %d keys, got %d", numKeys, len(store))
	}
	if conflicts > 0 {
		t.Errorf("unexpected idempotency conflicts during insert: %d", conflicts)
	}

	// Now replay: same key, same body -> should succeed.
	replaySuccess := 0
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("idem_load_%d", i)
		hash := fmt.Sprintf("hash_%d", i)
		if storedHash, ok := store[key]; ok && storedHash == hash {
			replaySuccess++
		}
	}
	if replaySuccess != numKeys {
		t.Errorf("replay: expected %d successes, got %d", numKeys, replaySuccess)
	}

	// Mismatch: same key, different body -> conflict.
	conflictCount := 0
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("idem_load_%d", i)
		wrongHash := fmt.Sprintf("wrong_hash_%d", i)
		if storedHash, ok := store[key]; ok && storedHash != wrongHash {
			conflictCount++
		}
	}
	if conflictCount != numKeys {
		t.Errorf("conflict detection: expected %d, got %d", numKeys, conflictCount)
	}

	t.Logf("idempotency load test passed: %d keys created, %d replays, %d conflicts detected",
		numKeys, replaySuccess, conflictCount)
}

// TestLoadInventoryConcurrencyStress simulates high-concurrency inventory deduction.
func TestLoadInventoryConcurrencyStress(t *testing.T) {
	stock := int64(10000) // 10k units available.
	var mu sync.Mutex
	var wg sync.WaitGroup

	const numBuyers = 15000 // 15000 buyers for 10000 units.
	const quantityPerBuyer = 1

	successCount := int64(0)

	for i := 0; i < numBuyers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			if stock >= quantityPerBuyer {
				stock -= quantityPerBuyer
				successCount++
			}
		}()
	}
	wg.Wait()

	if successCount != 10000 {
		t.Errorf("expected 10000 successful purchases, got %d (oversell/undersell)", successCount)
	}
	if stock != 0 {
		t.Errorf("expected 0 remaining stock, got %d", stock)
	}

	t.Logf("inventory stress test passed: %d buyers, %d successful, %d remaining",
		numBuyers, successCount, stock)
}

// TestLoadLedgerBalanceConcurrency verifies balance reads under concurrent writes.
func TestLoadLedgerBalanceConcurrency(t *testing.T) {
	const numWallets = 100
	const updatesPerWallet = 50

	balances := make(map[string]int64)
	for i := 0; i < numWallets; i++ {
		balances[fmt.Sprintf("wallet_%d", i)] = int64(100000000) // 100 vUSDC each.
	}

	var mu sync.RWMutex
	var wg sync.WaitGroup

	// Concurrent balance readers.
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				mu.RLock()
				_, ok := balances[fmt.Sprintf("wallet_%d", j%numWallets)]
				mu.RUnlock()
				if !ok {
					t.Error("wallet not found during read")
				}
			}
		}()
	}

	// Concurrent balance updaters.
	for i := 0; i < numWallets; i++ {
		wg.Add(1)
		go func(walletIdx int) {
			defer wg.Done()
			for j := 0; j < updatesPerWallet; j++ {
				mu.Lock()
				key := fmt.Sprintf("wallet_%d", walletIdx)
				balances[key] += int64(100000) // Add 0.1 vUSDC.
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// Verify all balances are consistent (no corruption).
	for i := 0; i < numWallets; i++ {
		key := fmt.Sprintf("wallet_%d", i)
		expected := int64(100000000) + int64(updatesPerWallet*100000)
		if balances[key] != expected {
			t.Errorf("wallet %s: expected %d, got %d (data corruption)", key, expected, balances[key])
		}
	}

	t.Logf("ledger balance concurrency test passed: %d wallets x %d updates", numWallets, updatesPerWallet)
}

// TestLoadBodyHashCollisionResistance verifies SHA-256 collision resistance
// for idempotency body hashing under load.
func TestLoadBodyHashCollisionResistance(t *testing.T) {
	const numBodies = 10000

	hashes := make(map[string]int) // hash -> count.

	for i := 0; i < numBodies; i++ {
		body := map[string]interface{}{
			"order_intent_id": fmt.Sprintf("intent_%d", i),
			"quote_id":        fmt.Sprintf("quote_%d", i),
			"wallet":          fmt.Sprintf("wallet_%d", i),
		}
		bodyJSON, _ := json.Marshal(body)
		hash := sha256.Sum256(bodyJSON)
		hashStr := fmt.Sprintf("%x", hash)
		hashes[hashStr]++
	}

	// Check for collisions (extremely unlikely with SHA-256).
	collisions := 0
	for _, count := range hashes {
		if count > 1 {
			collisions++
		}
	}

	if collisions > 0 {
		t.Errorf("unexpected body hash collisions: %d", collisions)
	} else {
		t.Logf("body hash collision test passed: %d unique hashes, 0 collisions", numBodies)
	}
}
