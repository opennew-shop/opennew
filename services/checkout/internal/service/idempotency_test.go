package service

import (
	"testing"
)

// TestComputeBodyHash verifies that ComputeBodyHash produces:
//   - Consistent output for identical input
//   - Different output for different input
//   - Non-empty output for empty input
// 验证：相同输入哈希一致、不同输入哈希不同、空输入仍产出非空哈希。
func TestComputeBodyHash(t *testing.T) {
	// Same body must produce same hash.
	t.Run("consistent hash for identical bodies", func(t *testing.T) {
		body1 := map[string]interface{}{"order_intent_id": "intent_abc", "quote_id": "quote_xyz"}
		body2 := map[string]interface{}{"order_intent_id": "intent_abc", "quote_id": "quote_xyz"}
		h1, err1 := ComputeBodyHash(body1)
		h2, err2 := ComputeBodyHash(body2)
		if err1 != nil {
			t.Fatalf("ComputeBodyHash(body1) error: %v", err1)
		}
		if err2 != nil {
			t.Fatalf("ComputeBodyHash(body2) error: %v", err2)
		}
		if h1 != h2 {
			t.Errorf("same body should produce same hash, got %s vs %s", h1, h2)
		}
	})

	// Different body must produce different hash.
	t.Run("different hash for different bodies", func(t *testing.T) {
		body1 := map[string]interface{}{"order_intent_id": "intent_abc", "quote_id": "quote_xyz"}
		body2 := map[string]interface{}{"order_intent_id": "intent_abc", "quote_id": "quote_DIFFERENT"}
		h1, err1 := ComputeBodyHash(body1)
		h2, err2 := ComputeBodyHash(body2)
		if err1 != nil {
			t.Fatalf("ComputeBodyHash(body1) error: %v", err1)
		}
		if err2 != nil {
			t.Fatalf("ComputeBodyHash(body2) error: %v", err2)
		}
		if h1 == h2 {
			t.Error("different bodies should produce different hashes")
		}
	})

	// Different key order (Go map) should still produce same hash
	// because json.Marshal sorts keys alphabetically.
	t.Run("key order does not affect hash", func(t *testing.T) {
		body1 := map[string]interface{}{"a": "1", "b": "2"}
		body2 := map[string]interface{}{"b": "2", "a": "1"}
		h1, _ := ComputeBodyHash(body1)
		h2, _ := ComputeBodyHash(body2)
		if h1 != h2 {
			t.Error("key order should not affect hash due to canonical JSON marshaling")
		}
	})

	// Empty body must produce a valid (non-empty) hash.
	t.Run("empty body produces valid hash", func(t *testing.T) {
		body := map[string]interface{}{}
		h, err := ComputeBodyHash(body)
		if err != nil {
			t.Fatalf("ComputeBodyHash(empty) error: %v", err)
		}
		if h == "" {
			t.Error("empty body should still produce a hash")
		}
	})

	// Nil body must produce a valid hash.
	t.Run("nil body produces valid hash", func(t *testing.T) {
		h, err := ComputeBodyHash(nil)
		if err != nil {
			t.Fatalf("ComputeBodyHash(nil) error: %v", err)
		}
		if h == "" {
			t.Error("nil body should still produce a hash")
		}
	})
}

// TestComputeBodyHashRaw verifies the raw-byte variant of ComputeBodyHash.
// 验证 ComputeBodyHashRaw（原始字节版）：相同字节哈希一致、不同字节哈希不同且非空。
func TestComputeBodyHashRaw(t *testing.T) {
	raw1 := []byte(`{"order_intent_id":"intent_abc","quote_id":"quote_xyz"}`)
	raw2 := []byte(`{"order_intent_id":"intent_abc","quote_id":"quote_xyz"}`)
	raw3 := []byte(`{"order_intent_id":"intent_abc","quote_id":"quote_DIFFERENT"}`)

	h1 := ComputeBodyHashRaw(raw1)
	h2 := ComputeBodyHashRaw(raw2)
	h3 := ComputeBodyHashRaw(raw3)

	if h1 != h2 {
		t.Error("same raw bytes should produce same hash")
	}
	if h1 == h3 {
		t.Error("different raw bytes should produce different hash")
	}
	if h1 == "" {
		t.Error("hash should not be empty")
	}
}

// TestIdempotencyResultConstants verifies the idempotency result enum values.
// 验证幂等结果枚举取值：New=0、Replay=1、Conflict=2。
func TestIdempotencyResultConstants(t *testing.T) {
	// Verify that IdempotencyNew is the zero value (iot = 0).
	if IdempotencyNew != 0 {
		t.Errorf("IdempotencyNew should be 0, got %d", IdempotencyNew)
	}
	if IdempotencyReplay != 1 {
		t.Errorf("IdempotencyReplay should be 1, got %d", IdempotencyReplay)
	}
	if IdempotencyConflict != 2 {
		t.Errorf("IdempotencyConflict should be 2, got %d", IdempotencyConflict)
	}
}

// TestIdempotencyReplayAndConflict documents the expected behavior of
// CheckAndResolveIdempotency (requires database).
// 说明 CheckAndResolveIdempotency 的预期行为（回放/冲突/新建），实际逻辑由集成测试覆盖。
// The actual implementation is tested via integration tests.
func TestIdempotencyReplayAndConflict(t *testing.T) {
	t.Run("idempotency replay returns cached response", func(t *testing.T) {
		t.Skip("requires database - run with -tags=integration")
	})

	t.Run("idempotency conflict when body differs", func(t *testing.T) {
		t.Skip("requires database - run with -tags=integration")
	})

	t.Run("new idempotency key proceeds normally", func(t *testing.T) {
		t.Skip("requires database - run with -tags=integration")
	})
}
