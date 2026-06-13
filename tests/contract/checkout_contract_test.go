// Package contract contains contract-level integration tests for the ANCF checkout pipeline.
// These tests verify the exported API surface matches the expectations defined in demo.md Section 14.
//
// Run with: go test -tags=integration ./tests/contract/
package contract

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"

	checkoutModel "github.com/ancf-commerce/ancf/services/checkout/internal/model"
	checkoutSvc "github.com/ancf-commerce/ancf/services/checkout/internal/service"
	ledgerModel "github.com/ancf-commerce/ancf/services/ledger/internal/model"
)

// TestEdDSAVerifyContract validates the signature verification contract:
//   - Valid signature must pass
//   - Tampered message must fail
//   - Invalid signature format must error
//   - Wrong public key must fail
func TestEdDSAVerifyContract(t *testing.T) {
	// Generate test Ed25519 key pair.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}
	walletAddr := hex.EncodeToString(pub)

	// Build a signable payload matching the SignablePayload struct.
	payload := map[string]interface{}{
		"domain":      "yourshop.com",
		"shop_id":     "zero_shop_sol_01",
		"network":     "solana-mainnet",
		"wallet":      walletAddr,
		"quote_id":    "quote_01JT000000000000000000000000",
		"total_minor": "4900000",
		"currency":    "vUSDC",
		"expires_at":  "2026-06-04T00:10:00Z",
		"nonce":       "abc123def4567890abc123def4567890",
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	// Build canonical message and sign.
	message, err := checkoutSvc.BuildSignableMessage(payload)
	if err != nil {
		t.Fatalf("BuildSignableMessage failed: %v", err)
	}
	sig := ed25519.Sign(priv, message)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	t.Run("valid signature passes", func(t *testing.T) {
		valid, err := checkoutSvc.VerifyEdDSASignature(json.RawMessage(payloadJSON), walletAddr, sigB64)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !valid {
			t.Error("valid signature should pass")
		}
	})

	t.Run("tampered message fails", func(t *testing.T) {
		tampered := map[string]interface{}{
			"domain":      "yourshop.com",
			"shop_id":     "zero_shop_sol_01",
			"network":     "solana-mainnet",
			"wallet":      walletAddr,
			"quote_id":    "quote_01JT000000000000000000000000",
			"total_minor": "9999999", // Tampered.
			"currency":    "vUSDC",
			"expires_at":  "2026-06-04T00:10:00Z",
			"nonce":       "abc123def4567890abc123def4567890",
		}
		tamperedJSON, _ := json.Marshal(tampered)
		valid, _ := checkoutSvc.VerifyEdDSASignature(json.RawMessage(tamperedJSON), walletAddr, sigB64)
		if valid {
			t.Error("tampered message must fail")
		}
	})

	t.Run("invalid base64 signature format", func(t *testing.T) {
		_, err := checkoutSvc.VerifyEdDSASignature(json.RawMessage(payloadJSON), walletAddr, "!!!invalid!!!")
		if err == nil {
			t.Error("invalid base64 must return error")
		}
	})

	t.Run("wrong public key fails", func(t *testing.T) {
		wrongPub, _, _ := ed25519.GenerateKey(rand.Reader)
		wrongAddr := hex.EncodeToString(wrongPub)
		valid, _ := checkoutSvc.VerifyEdDSASignature(json.RawMessage(payloadJSON), wrongAddr, sigB64)
		if valid {
			t.Error("wrong public key must fail")
		}
	})
}

// TestOrderStateMachineContract validates the order state machine contract:
// all legal transitions pass, all illegal transitions are rejected.
func TestOrderStateMachineContract(t *testing.T) {
	tests := []struct {
		name  string
		from  string
		to    string
		valid bool
	}{
		// Legal transitions.
		{"created->prepared", checkoutModel.StatusCreated, checkoutModel.StatusPrepared, true},
		{"prepared->committed", checkoutModel.StatusPrepared, checkoutModel.StatusCommitted, true},
		{"committed->paid", checkoutModel.StatusCommitted, checkoutModel.StatusPaid, true},
		{"paid->provisioning", checkoutModel.StatusPaid, checkoutModel.StatusProvisioning, true},
		{"provisioning->completed", checkoutModel.StatusProvisioning, checkoutModel.StatusCompleted, true},
		{"prepared->failed", checkoutModel.StatusPrepared, checkoutModel.StatusFailed, true},
		{"committed->failed", checkoutModel.StatusCommitted, checkoutModel.StatusFailed, true},
		{"paid->failed", checkoutModel.StatusPaid, checkoutModel.StatusFailed, true},
		{"provisioning->failed", checkoutModel.StatusProvisioning, checkoutModel.StatusFailed, true},
		{"failed->refunded", checkoutModel.StatusFailed, checkoutModel.StatusRefunded, true},
		// Illegal transitions.
		{"created->committed (skip)", checkoutModel.StatusCreated, checkoutModel.StatusCommitted, false},
		{"prepared->completed (skip)", checkoutModel.StatusPrepared, checkoutModel.StatusCompleted, false},
		{"committed->prepared (backwards)", checkoutModel.StatusCommitted, checkoutModel.StatusPrepared, false},
		{"completed->failed (terminal)", checkoutModel.StatusCompleted, checkoutModel.StatusFailed, false},
		{"refunded->created (terminal)", checkoutModel.StatusRefunded, checkoutModel.StatusCreated, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkoutSvc.ValidateTransition(tc.from, tc.to)
			if tc.valid && err != nil {
				t.Errorf("should be valid: %v", err)
			}
			if !tc.valid && err == nil {
				t.Error("should be invalid")
			}
		})
	}
}

// TestIdempotencyKeyContract documents the idempotency key contract requirements.
// Full integration tests require a database.
func TestIdempotencyKeyContract(t *testing.T) {
	t.Run("same idempotency key with same body replays response", func(t *testing.T) {
		t.Skip("requires database - run with -tags=integration")
	})

	t.Run("same idempotency key with different body returns 409 conflict", func(t *testing.T) {
		t.Skip("requires database - run with -tags=integration")
	})

	t.Run("new idempotency key processes normally", func(t *testing.T) {
		t.Skip("requires database - run with -tags=integration")
	})
}

// TestComputeBodyHashContract verifies the body hash contract at the API boundary.
func TestComputeBodyHashContract(t *testing.T) {
	body1 := map[string]interface{}{"order_intent_id": "intent_abc", "quote_id": "quote_xyz"}
	body2 := map[string]interface{}{"order_intent_id": "intent_abc", "quote_id": "quote_xyz"}

	h1, _ := checkoutSvc.ComputeBodyHash(body1)
	h2, _ := checkoutSvc.ComputeBodyHash(body2)

	if h1 != h2 {
		t.Error("identical bodies must produce identical hashes for idempotency")
	}

	body3 := map[string]interface{}{"order_intent_id": "intent_abc", "quote_id": "different"}
	h3, _ := checkoutSvc.ComputeBodyHash(body3)

	if h1 == h3 {
		t.Error("different bodies must produce different hashes")
	}
}

// TestLedgerDoubleEntryContract verifies the double-entry accounting invariants.
func TestLedgerDoubleEntryContract(t *testing.T) {
	t.Run("purchase hold double entry", func(t *testing.T) {
		entries := ledgerModel.PurchaseHold("tx_h1", "wallet_A", 5000000, "vUSDC", "intent_1")
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry pair, got %d", len(entries))
		}
		if !ledgerModel.ValidateBalance(entries) {
			t.Error("purchase hold must balance (debit == credit)")
		}
	})

	t.Run("purchase settle double entry", func(t *testing.T) {
		entries := ledgerModel.PurchaseSettle("tx_s1", "wallet_A", 5000000, "vUSDC", "intent_1")
		if !ledgerModel.ValidateBalance(entries) {
			t.Error("purchase settle must balance")
		}
	})

	t.Run("purchase refund double entry", func(t *testing.T) {
		entries := ledgerModel.PurchaseRefund("tx_r1", "wallet_A", 5000000, "vUSDC", "intent_1")
		if !ledgerModel.ValidateBalance(entries) {
			t.Error("purchase refund must balance")
		}
	})

	t.Run("mint credit 4-entry transaction", func(t *testing.T) {
		entries := ledgerModel.MintCredit("tx_m1", "wallet_A", 1000000, "vUSDC", "deposit_1")
		if len(entries) != 2 {
			t.Fatalf("expected 2 entry pairs, got %d", len(entries))
		}
		if !ledgerModel.ValidateBalance(entries) {
			t.Error("mint credit must balance across both pairs")
		}
	})

	t.Run("redemption debit 4-entry transaction", func(t *testing.T) {
		entries := ledgerModel.RedemptionDebit("tx_rd1", "wallet_A", 500000, "vUSDC", "redemption_1")
		if len(entries) != 2 {
			t.Fatalf("expected 2 entry pairs, got %d", len(entries))
		}
		if !ledgerModel.ValidateBalance(entries) {
			t.Error("redemption debit must balance across both pairs")
		}
	})
}

// TestWalletAddressFormatContract validates the Ed25519 public key derivation contract.
func TestWalletAddressFormatContract(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	t.Run("hex address without 0x prefix", func(t *testing.T) {
		addr := hex.EncodeToString(pub)
		derived, err := checkoutSvc.DerivePublicKey(addr)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(derived) != ed25519.PublicKeySize {
			t.Errorf("wrong key size: %d", len(derived))
		}
	})

	t.Run("hex address with 0x prefix", func(t *testing.T) {
		addr := "0x" + hex.EncodeToString(pub)
		derived, err := checkoutSvc.DerivePublicKey(addr)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(derived) != ed25519.PublicKeySize {
			t.Errorf("wrong key size: %d", len(derived))
		}
	})

	t.Run("invalid length fails", func(t *testing.T) {
		_, err := checkoutSvc.DerivePublicKey("short")
		if err == nil {
			t.Error("short address must return error")
		}
	})

	t.Run("invalid hex characters fail", func(t *testing.T) {
		_, err := checkoutSvc.DerivePublicKey("gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg")
		if err == nil {
			t.Error("non-hex address must return error")
		}
	})
}
