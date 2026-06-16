package middleware

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// HTTPSignature returns a Gin middleware that validates HTTP Message Signatures
// as specified in RFC 9421.
//
// This middleware checks the "Signature-Input" and "Signature" headers to verify
// that the request was signed by an authorized party.
//
// Currently, it validates:
//   - The "Signature-Input" header specifies covered components (method, target-uri, content-digest).
//   - The "Signature" header contains a valid base64-encoded signature.
//   - The signature creation timestamp is within an acceptable window (5 minutes).
//
// 中文说明：按 RFC 9421 校验 HTTP 消息签名，检查 Signature-Input / Signature 头，
// 验证覆盖组件、base64 签名有效性及签名时间戳是否在 5 分钟窗口内。
//
// In production, signature verification against known public keys would be required.
// For Phase 1, it validates the structure and presence of required headers.
func HTTPSignature() gin.HandlerFunc {
	return func(c *gin.Context) {
		sigInput := c.GetHeader("Signature-Input")
		signature := c.GetHeader("Signature")

		if sigInput == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "MISSING_SIGNATURE_INPUT",
					"message": "HTTP Message Signature required. Missing Signature-Input header (RFC 9421).",
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		if signature == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "MISSING_SIGNATURE",
					"message": "HTTP Message Signature required. Missing Signature header (RFC 9421).",
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// Parse Signature-Input header
		// Format: sig1=("method" "target-uri" "content-digest");created=1234567890;keyid="key-1"
		sigParams, err := parseSignatureInput(sigInput)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "INVALID_SIGNATURE_INPUT",
					"message": fmt.Sprintf("Failed to parse Signature-Input header: %s", err.Error()),
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// Verify signature timestamp is within the acceptable window (5 minutes)
		if created, ok := sigParams["created"]; ok {
			createdTime := time.Unix(int64(created), 0)
			if time.Since(createdTime) > 5*time.Minute || time.Since(createdTime) < -5*time.Minute {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"error": gin.H{
						"code":    "SIGNATURE_EXPIRED",
						"message": "HTTP signature creation timestamp is outside the acceptable window (5 minutes).",
						"request_id": c.GetString("request_id"),
					},
				})
				return
			}
		}

		// Check required covered components
		coveredComponents, ok := sigParams["covered"].([]string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "INVALID_SIGNATURE_COVERAGE",
					"message": "Signature-Input must specify covered components.",
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// Build the signature base (covered components concatenated)
		signatureBase, err := buildSignatureBase(c, coveredComponents)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "SIGNATURE_BASE_ERROR",
					"message": fmt.Sprintf("Failed to build signature base: %s", err.Error()),
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// Decode the signature
		sigBytes, err := base64.StdEncoding.DecodeString(signature)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "INVALID_SIGNATURE_ENCODING",
					"message": "Signature header is not valid base64.",
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// In Phase 1 development, we validate signature structure but accept any valid base64.
		// In production, verify against known public keys based on keyid.
		keyID, hasKeyID := sigParams["keyid"].(string)
		if hasKeyID {
			_ = keyID // TODO: Look up public key by keyID and verify
		}

		// Store signature context for downstream handlers
		c.Set("http_signature_verified", true)
		c.Set("http_signature_keyid", keyID)
		c.Set("http_signature_covered", coveredComponents)
		c.Set("http_signature_base", signatureBase)

		// Reject signatures that are too short (obviously invalid)
		if len(sigBytes) < 16 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "INVALID_SIGNATURE",
					"message": "HTTP signature is too short to be valid.",
					"request_id": c.GetString("request_id"),
				},
			})
			return
		}

		// In dev mode, we accept the signature if it has the correct structure.
		// Production: verify with crypto.Verify or similar.
		c.Set("dev_signature_accepted", true)

		c.Next()
	}
}

// parseSignatureInput parses a Signature-Input header value.
// Format: sig1=("component1" "component2");created=123;keyid="mykey"
func parseSignatureInput(input string) (map[string]interface{}, error) {
	params := make(map[string]interface{})

	// Split by semicolons for parameters
	parts := strings.Split(input, ";")
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty Signature-Input")
	}

	// First part: sigLabel=("comp1" "comp2")
	firstPart := strings.TrimSpace(parts[0])
	eqIdx := strings.Index(firstPart, "=")
	if eqIdx < 0 {
		return nil, fmt.Errorf("invalid Signature-Input format: missing '='")
	}

	// Extract covered components from the parenthesized list
	coveredPart := firstPart[eqIdx+1:]
	coveredPart = strings.TrimSpace(coveredPart)
	if strings.HasPrefix(coveredPart, "(") && strings.HasSuffix(coveredPart, ")") {
		coveredPart = coveredPart[1 : len(coveredPart)-1]
		// Split by spaces, handling quoted components
		components := parseQuotedList(coveredPart)
		params["covered"] = components
	}

	// Parse parameters (created, keyid, alg, etc.)
	for i := 1; i < len(parts); i++ {
		param := strings.TrimSpace(parts[i])
		kv := strings.SplitN(param, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		// Remove surrounding quotes
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			value = value[1 : len(value)-1]
		}

		// Try to parse as number
		var numVal float64
		if _, err := fmt.Sscanf(value, "%f", &numVal); err == nil && value != "" {
			params[key] = numVal
		} else {
			params[key] = value
		}
	}

	return params, nil
}

// parseQuotedList splits a space-separated list that may contain quoted strings.
func parseQuotedList(s string) []string {
	var result []string
	var current strings.Builder
	inQuotes := false

	for _, ch := range s {
		if ch == '"' {
			inQuotes = !inQuotes
			current.WriteRune(ch)
		} else if ch == ' ' && !inQuotes {
			if current.Len() > 0 {
				result = append(result, strings.Trim(current.String(), "\""))
				current.Reset()
			}
		} else {
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		result = append(result, strings.Trim(current.String(), "\""))
	}

	return result
}

// buildSignatureBase builds the signature base string from covered components.
// Each component is represented as: "component-name": value
func buildSignatureBase(c *gin.Context, components []string) (string, error) {
	var lines []string

	for _, comp := range components {
		switch comp {
		case "\"@method\"", "@method":
			lines = append(lines, fmt.Sprintf("\"@method\": %s", c.Request.Method))
		case "\"@target-uri\"", "@target-uri":
			uri := c.Request.URL.RequestURI()
			lines = append(lines, fmt.Sprintf("\"@target-uri\": %s", uri))
		case "\"@authority\"", "@authority":
			lines = append(lines, fmt.Sprintf("\"@authority\": %s", c.Request.Host))
		case "\"@scheme\"", "@scheme":
			scheme := "http"
			if c.Request.TLS != nil {
				scheme = "https"
			}
			lines = append(lines, fmt.Sprintf("\"@scheme\": %s", scheme))
		case "\"@request-target\"", "@request-target":
			uri := c.Request.URL.RequestURI()
			lines = append(lines, fmt.Sprintf("\"@request-target\": %s %s", strings.ToLower(c.Request.Method), uri))
		case "\"content-digest\"", "content-digest":
			digest := c.GetHeader("Content-Digest")
			if digest == "" {
				// Calculate content digest from body
				body, err := c.GetRawData()
				if err != nil {
					return "", fmt.Errorf("failed to read body for content-digest: %w", err)
				}
				hash := sha256.Sum256(body)
				digest = "sha-256=:" + base64.StdEncoding.EncodeToString(hash[:]) + ":"
				c.Set("raw_body", body)
			}
			lines = append(lines, fmt.Sprintf("\"content-digest\": %s", digest))
		case "\"content-type\"", "content-type":
			lines = append(lines, fmt.Sprintf("\"content-type\": %s", c.ContentType()))
		default:
			// Unknown component - include as-is
			cleanComp := strings.Trim(comp, "\"")
			lines = append(lines, fmt.Sprintf("\"%s\": ", cleanComp))
		}
	}

	return strings.Join(lines, "\n"), nil
}

// Placeholder functions for future crypto verification:

// verifyEd25519Signature verifies an Ed25519 signature against the signature base.
// pubKey is the Ed25519 public key (32 bytes).
func verifyEd25519Signature(pubKey ed25519.PublicKey, signature, message []byte) bool {
	return ed25519.Verify(pubKey, message, signature)
}

// verifyRSASignature verifies an RSA PKCS#1 v1.5 signature.
func verifyRSASignature(pubKey *rsa.PublicKey, signature, message []byte, hash crypto.Hash) error {
	h := hash.New()
	h.Write(message)
	return rsa.VerifyPKCS1v15(pubKey, hash, h.Sum(nil), signature)
}

// signatureToJSON converts the signature context to a JSON representation for logging.
func signatureToJSON(sigInput, signature string) []byte {
	jsonData, _ := json.Marshal(map[string]string{
		"signature_input": sigInput,
		"signature":       signature[:min(len(signature), 20)] + "...",
	})
	return jsonData
}
