package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/api-gateway/internal/config"
)

// ServeManifest returns a Gin handler that serves the ANCF discovery manifest
// at GET /.well-known/agent-rules.json.
//
// The manifest contains all information an Agent needs to discover and interact
// with the shop: protocol version, capabilities, schemas, firmware, agent policy,
// payment rails, and cryptographic signature.
//
// Content is defined per demo.md Section 5 (Discovery Manifest) and Section 16
// (Alipay A2A Payment Rail).
func ServeManifest(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		now := time.Now().UTC()
		issuedAt := now.Format(time.RFC3339)
		expiresAt := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)

		shopID := cfg.ShopID
		if shopID == "" {
			shopID = "zero_shop_sol_01"
		}

		manifest := gin.H{
			"protocol_version":   "ANCF-1.0",
			"shop_id":            shopID,
			"issued_at":          issuedAt,
			"expires_at":         expiresAt,
			"supported_networks": []string{"solana-mainnet", "sonic-l2"},
			"supported_assets": []gin.H{
				{
					"symbol":     "vUSDC",
					"decimals":   6,
					"type":       "shadow-ledger",
					"redeemable": true,
				},
			},
			"schemas": gin.H{
				"manifest": "https://cdn.yourshop.com/ancf/v1/manifest.schema.json",
				"checkout": "https://cdn.yourshop.com/ancf/v1/checkout.schema.json",
				"mint":     "https://cdn.yourshop.com/ancf/v1/mint.schema.json",
			},
			"capabilities": gin.H{
				"search": gin.H{
					"endpoint": "/api/v1/cli/search",
					"method":   "GET",
				},
				"quote": gin.H{
					"endpoint": "/api/v1/cli/quote",
					"method":   "POST",
				},
				"checkout_prepare": gin.H{
					"endpoint": "/api/v1/cli/checkout/prepare",
					"method":   "POST",
				},
				"checkout_commit": gin.H{
					"endpoint":                 "/api/v1/cli/checkout/commit",
					"method":                   "POST",
					"requires_idempotency_key": true,
					"requires_wallet_signature": true,
				},
				"deposit_intent": gin.H{
					"endpoint": "/api/v1/wallet/deposit-intents",
					"method":   "POST",
				},
				"redeem": gin.H{
					"endpoint": "/api/v1/wallet/redeem",
					"method":   "POST",
				},
			},
			"ui_firmware": gin.H{
				"components": []gin.H{
					// SECURITY FIX: F-006-02 — Real sha384 SRI hashes computed from firmware/components/dist/
					{
						"url":       "https://cdn.yourshop.com/firmware/v1/agent-bridge.js",
						"integrity": "sha384-cU1HIpaeViVCuKbIg8yFU5sZG0qc2NBJxls8vnWsTpj5WeZuQbkHLBRcI+AWkgwj",
						"type":      "module",
					},
					{
						"url":       "https://cdn.yourshop.com/firmware/v1/ancf-search.js",
						"integrity": "sha384-j2BIp5ZHftD2WNlB0xx5zitrE6d1mzz3JidoSVshboijm/elysSumRE2ru/RYTIf",
						"type":      "module",
					},
					{
						"url":       "https://cdn.yourshop.com/firmware/v1/ancf-quote.js",
						"integrity": "sha384-O3FdTNspnBfZm/JwgTGpqE0J0aEizdJdOH6Zo6hOTU7qrfU8kZZXUdlT2oEE2QPK",
						"type":      "module",
					},
					{
						"url":       "https://cdn.yourshop.com/firmware/v1/ancf-checkout.js",
						"integrity": "sha384-G0DAjIg1oXn36VPgYIbAUPZRmfen7/OaTVSRGG/w51BH5U5NDYxJAcB0HWshimP3",
						"type":      "module",
					},
					{
						"url":       "https://cdn.yourshop.com/firmware/v1/ancf-theme.js",
						"integrity": "sha384-WouT7fyAuBWfhVcr97ylVuQFB3ggfT4KMTnm+7Fo+HFcQ2X9Q2KsB7R75AeMyOYi",
						"type":      "script",
					},
				},
				"theme_tokens": gin.H{
					"primary":    "#00FFA3",
					"background": "#0D0E12",
					"text":       "#FFFFFF",
				},
			},
			"agent_policy": gin.H{
				"allow_autonomous_checkout":  false,
				"max_auto_total_minor":       "0",
				"require_human_confirmation": true,
				"allowed_component_hosts":    []string{"cdn.yourshop.com"},
			},
			"payment_rails": []gin.H{
				{
					"rail":                         "alipay_a2a",
					"currency":                     "CNY",
					"capabilities":                 []string{"direct_checkout", "deposit_topup", "usage_charge"},
					"requires_user_authorization":  true,
					"payment_skill":                "alipay_payment_skill",
					"preserve_payment_url_exactly": true,
				},
				{
					"rail":                        "vusdc_ledger",
					"currency":                    "vUSDC",
					"capabilities":                []string{"direct_checkout"},
					"requires_user_authorization": true,
				},
			},
			"signature": gin.H{
				"alg": "EdDSA",
				"kid": "firmware-key-2026-06",
				"jws": "eyJhbGciOiJFZERTQSJ9...dev_placeholder_signature",
			},
		}

		c.JSON(http.StatusOK, manifest)
	}
}
