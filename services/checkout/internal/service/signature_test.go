package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// TestVerifyEdDSASignature verifies the full EdDSA signature verification pipeline.
// It tests 4 cases:
//   1. Valid signature passes
//   2. Tampered message fails
//   3. Invalid base64 signature format returns error
//   4. Wrong public key fails
// 验证完整 EdDSA 校验流程：有效签名通过、被篡改消息失败、非法 base64 报错、错误公钥失败。
func TestVerifyEdDSASignature(t *testing.T) {
	// Generate test Ed25519 key pair.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}
	walletAddr := hex.EncodeToString(pub) // 64 hex chars = 32 bytes

	// Build the signable payload (matches SignablePayload struct shape).
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

	// Build the canonical signable message and sign it with the private key.
	message, err := BuildSignableMessage(payload)
	if err != nil {
		t.Fatalf("failed to build signable message: %v", err)
	}
	sig := ed25519.Sign(priv, message)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	// Case 1: Valid signature must pass verification.
	t.Run("valid signature passes", func(t *testing.T) {
		valid, err := VerifyEdDSASignature(json.RawMessage(payloadJSON), walletAddr, sigB64)
		if err != nil {
			t.Errorf("unexpected error for valid signature: %v", err)
		}
		if !valid {
			t.Error("valid signature should pass verification")
		}
	})

	// Case 2: Tampered message (wrong total_minor) must fail.
	t.Run("tampered message fails", func(t *testing.T) {
		tamperedPayload := map[string]interface{}{
			"domain":      "yourshop.com",
			"shop_id":     "zero_shop_sol_01",
			"network":     "solana-mainnet",
			"wallet":      walletAddr,
			"quote_id":    "quote_01JT000000000000000000000000",
			"total_minor": "9999999", // Tampered amount.
			"currency":    "vUSDC",
			"expires_at":  "2026-06-04T00:10:00Z",
			"nonce":       "abc123def4567890abc123def4567890",
		}
		tamperedJSON, _ := json.Marshal(tamperedPayload)
		valid, err := VerifyEdDSASignature(json.RawMessage(tamperedJSON), walletAddr, sigB64)
		if err != nil {
			t.Errorf("unexpected error for tampered message: %v", err)
		}
		if valid {
			t.Error("tampered message should fail verification")
		}
	})

	// Case 3: Invalid base64 signature must return an error.
	t.Run("invalid base64 signature format", func(t *testing.T) {
		_, err := VerifyEdDSASignature(json.RawMessage(payloadJSON), walletAddr, "not-valid-base64!!!")
		if err == nil {
			t.Error("invalid base64 signature should return error")
		}
	})

	// Case 4: Wrong public key must fail verification.
	t.Run("wrong public key fails", func(t *testing.T) {
		wrongPub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("failed to generate wrong key pair: %v", err)
		}
		wrongWalletAddr := hex.EncodeToString(wrongPub)
		valid, err := VerifyEdDSASignature(json.RawMessage(payloadJSON), wrongWalletAddr, sigB64)
		if err != nil {
			t.Errorf("unexpected error for wrong public key: %v", err)
		}
		if valid {
			t.Error("wrong public key should fail verification")
		}
	})
}

// TestDerivePublicKey validates the hex wallet address to Ed25519 public key derivation.
// 验证由十六进制钱包地址（含/不含 0x 前缀）派生 Ed25519 公钥，并对非法长度/字符报错。
func TestDerivePublicKey(t *testing.T) {
	t.Run("valid hex address without prefix", func(t *testing.T) {
		// 32 bytes of random data, hex-encoded = 64 chars.
		pub, _, _ := ed25519.GenerateKey(rand.Reader)
		hexAddr := hex.EncodeToString(pub)
		derived, err := DerivePublicKey(hexAddr)
		if err != nil {
			t.Errorf("valid hex address should derive without error: %v", err)
		}
		if len(derived) != ed25519.PublicKeySize {
			t.Errorf("derived key length %d, expected %d", len(derived), ed25519.PublicKeySize)
		}
	})

	t.Run("valid hex address with 0x prefix", func(t *testing.T) {
		pub, _, _ := ed25519.GenerateKey(rand.Reader)
		hexAddr := "0x" + hex.EncodeToString(pub)
		derived, err := DerivePublicKey(hexAddr)
		if err != nil {
			t.Errorf("0x-prefixed hex address should derive without error: %v", err)
		}
		if len(derived) != ed25519.PublicKeySize {
			t.Errorf("derived key length %d, expected %d", len(derived), ed25519.PublicKeySize)
		}
	})

	t.Run("invalid length address", func(t *testing.T) {
		_, err := DerivePublicKey("too_short")
		if err == nil {
			t.Error("short address should return error")
		}
	})

	t.Run("invalid hex characters", func(t *testing.T) {
		_, err := DerivePublicKey("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")
		if err == nil {
			t.Error("invalid hex characters should return error")
		}
	})
}

// TestCanonicalJSON verifies that JSON marshaling produces deterministic output.
// 验证规范化 JSON 序列化按键名字母序输出，结果确定可复现。
func TestCanonicalJSON(t *testing.T) {
	// Go's json.Marshal sorts map keys alphabetically.
	v := map[string]interface{}{
		"z_key": "last",
		"a_key": "first",
		"m_key": "middle",
	}
	result, err := CanonicalJSON(v)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}

	expected := `{"a_key":"first","m_key":"middle","z_key":"last"}`
	if string(result) != expected {
		t.Errorf("CanonicalJSON = %s, expected %s", string(result), expected)
	}
}

// TestBuildSignableMessage verifies the message format for wallet signing.
// 验证待签名消息格式以 "ANCF_CHECKOUT:" 前缀拼接规范化 JSON。
func TestBuildSignableMessage(t *testing.T) {
	payload := map[string]interface{}{
		"currency":    "vUSDC",
		"total_minor": "4900000",
	}
	msg, err := BuildSignableMessage(payload)
	if err != nil {
		t.Fatalf("BuildSignableMessage failed: %v", err)
	}

	msgStr := string(msg)
	expectedPrefix := "ANCF_CHECKOUT:{"
	if msgStr[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("message should start with %q, got %q", expectedPrefix, msgStr)
	}
}
