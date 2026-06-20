package service

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
)

// VerifySignature verifies an EdDSA (Ed25519) signature.
// publicKey is the 32-byte Ed25519 public key (derived from wallet address).
// message is the canonical JSON signable message bytes.
// signatureB64 is the base64-encoded 64-byte Ed25519 signature.
// Returns true if and only if the signature is cryptographically valid.
func VerifySignature(publicKey []byte, message []byte, signatureB64 string) (bool, error) {
	sig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return false, fmt.Errorf("invalid signature encoding: %w", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return false, fmt.Errorf("invalid public key size: %d", len(publicKey))
	}
	return ed25519.Verify(publicKey, message, sig), nil
}

// CanonicalJSON generates canonical JSON by marshaling the value.
// Go's json.Marshal sorts map keys alphabetically, producing canonical output.
func CanonicalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// BuildSignableMessage constructs the message the wallet must sign.
// Format: "ANCF_CHECKOUT:{canonical_json_of_payload}"
// The payload map is marshaled with sorted keys for deterministic output.
func BuildSignableMessage(payload map[string]interface{}) ([]byte, error) {
	cj, err := CanonicalJSON(payload)
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("ANCF_CHECKOUT:%s", string(cj))
	return []byte(msg), nil
}

// VerifyEdDSASignature performs full EdDSA signature verification for a checkout commit.
//
// It:
//  1. Decodes the base64-encoded signature
//  2. Derives the 32-byte Ed25519 public key from the wallet address
//  3. Parses the signable_payload into a map for canonical key ordering
//  4. Builds the canonical signable message ("ANCF_CHECKOUT:{json}")
//  5. Verifies the Ed25519 signature against the public key and message
//
// Returns true only if all steps succeed and the signature is valid.
//
// 中文说明：checkout commit 的完整 EdDSA(Ed25519) 验签：解码签名→从钱包地址推导公钥→
// 解析 signable_payload 并按键排序构造规范消息("ANCF_CHECKOUT:{json}")→验签。
func VerifyEdDSASignature(signablePayload json.RawMessage, walletAddr string, signatureB64 string) (bool, error) {
	// Decode the base64 signature.
	sigBytes, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return false, fmt.Errorf("invalid signature encoding: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return false, fmt.Errorf("invalid signature size: expected %d bytes, got %d", ed25519.SignatureSize, len(sigBytes))
	}

	// Derive the 32-byte Ed25519 public key from the wallet address.
	pubKey, err := DerivePublicKey(walletAddr)
	if err != nil {
		return false, fmt.Errorf("failed to derive public key from wallet: %w", err)
	}

	// Parse the signable payload into a map for canonical key ordering.
	var payload map[string]interface{}
	if err := json.Unmarshal(signablePayload, &payload); err != nil {
		return false, fmt.Errorf("failed to parse signable payload: %w", err)
	}

	// Build the canonical signable message.
	message, err := BuildSignableMessage(payload)
	if err != nil {
		return false, fmt.Errorf("failed to build signable message: %w", err)
	}

	// Verify the Ed25519 signature.
	valid := ed25519.Verify(pubKey, message, sigBytes)
	return valid, nil
}

// DerivePublicKey extracts the 32-byte Ed25519 public key from a wallet address string.
// Supports:
//   - Hex-encoded addresses with or without "0x" prefix (64-66 chars)
//   - Solana base58 addresses (32-44 chars, most common for real wallets)
//
// Heuristic: 32-44 character strings are treated as base58; 64-66 are treated as hex.
func DerivePublicKey(walletAddr string) ([]byte, error) {
	addrLen := len(walletAddr)

	// SECURITY FIX: F-001-02 — Extended to support Solana base58 wallet addresses
	// (32-44 chars = base58, 64-66 chars = hex including optional 0x prefix).
	// 按地址长度启发式判别编码：32-44 视为 Solana base58，64-66 视为十六进制（可带 0x 前缀）。
	switch {
	case addrLen >= 32 && addrLen <= 44:
		// Solana base58 encoded public key (usually 32-44 chars).
		return base58Decode(walletAddr)
	case addrLen >= 64 && addrLen <= 66:
		// Hex-encoded public key (64 chars) with optional "0x" prefix (66 chars).
		hexStr := walletAddr
		if len(hexStr) >= 2 && hexStr[:2] == "0x" {
			hexStr = hexStr[2:]
		}
		if len(hexStr) != ed25519.PublicKeySize*2 {
			return nil, fmt.Errorf("wallet address length %d hex chars does not match expected %d (64 hex = 32 bytes)", len(hexStr), ed25519.PublicKeySize*2)
		}
		pubKey, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("wallet address is not a valid hex-encoded Ed25519 public key: %w", err)
		}
		return pubKey, nil
	default:
		return nil, fmt.Errorf("wallet address length %d unrecognized; expected 32-44 chars (base58) or 64-66 chars (hex)", addrLen)
	}
}

// base58Alphabet is the Bitcoin/IPFS base58 alphabet.
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// base58Decode decodes a base58-encoded string into bytes.
// Used for Solana wallet address decoding.
func base58Decode(encoded string) ([]byte, error) {
	// Build an index map for character lookup.
	charIndex := make(map[byte]int, 58)
	for i := 0; i < len(base58Alphabet); i++ {
		charIndex[base58Alphabet[i]] = i
	}

	fiftyEight := big.NewInt(58)

	result := big.NewInt(0)
	for i := 0; i < len(encoded); i++ {
		idx, ok := charIndex[encoded[i]]
		if !ok {
			return nil, fmt.Errorf("invalid base58 character 0x%x at position %d", encoded[i], i)
		}
		result.Mul(result, fiftyEight)
		result.Add(result, big.NewInt(int64(idx)))
	}

	// Count leading base58 zeros (character '1').
	leadingZeros := 0
	for leadingZeros < len(encoded) && encoded[leadingZeros] == '1' {
		leadingZeros++
	}

	decoded := result.Bytes()
	// Prepend leading zero bytes.
	final := make([]byte, leadingZeros+len(decoded))
	copy(final[leadingZeros:], decoded)

	// Validate decoded length: Ed25519 public key is 32 bytes.
	if len(final) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("base58-decoded wallet address length %d does not match expected %d (32 bytes)", len(final), ed25519.PublicKeySize)
	}

	return final, nil
}

// ComputeBodyHash computes the SHA-256 hash of a request body for idempotency tracking.
// The body is first marshaled to JSON, then hashed.
func ComputeBodyHash(body interface{}) (string, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body for hashing: %w", err)
	}
	hash := sha256.Sum256(bodyBytes)
	return fmt.Sprintf("%x", hash), nil
}

// ComputeBodyHashRaw computes the SHA-256 hash of raw bytes for idempotency tracking.
func ComputeBodyHashRaw(body []byte) string {
	hash := sha256.Sum256(body)
	return fmt.Sprintf("%x", hash)
}
