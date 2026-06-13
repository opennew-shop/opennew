package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/checkout/internal/model"
	"github.com/ancf-commerce/ancf/services/checkout/internal/service"
)

// classifyCommitError maps an error message from CommitCheckout to an HTTP status code.
// This provides granular error classification for client-side handling.
func classifyCommitError(errMsg string) int {
	// 410 Gone: quote expired or already consumed.
	if strings.Contains(errMsg, "expired") || strings.Contains(errMsg, "already been consumed") {
		return http.StatusGone
	}

	// 404 Not Found: intent or quote not found.
	if strings.Contains(errMsg, "not found") {
		return http.StatusNotFound
	}

	// 409 Conflict: idempotency conflict, state transition violation, concurrent conflict.
	if strings.Contains(errMsg, "idempotency") && (strings.Contains(errMsg, "different request") || strings.Contains(errMsg, "conflict")) {
		return http.StatusConflict
	}
	if strings.Contains(errMsg, "concurrent conflict") {
		return http.StatusConflict
	}
	if strings.Contains(errMsg, "invalid state transition") || strings.Contains(errMsg, "invalid transition") {
		return http.StatusConflict
	}
	if strings.Contains(errMsg, "status changed") || strings.Contains(errMsg, "state") {
		return http.StatusConflict
	}

	// 403 Forbidden: wallet mismatch or signature verification failure.
	if strings.Contains(errMsg, "wallet") && (strings.Contains(errMsg, "does not match") || strings.Contains(errMsg, "mismatch")) {
		return http.StatusForbidden
	}
	if strings.Contains(errMsg, "signature") {
		return http.StatusForbidden
	}

	// 400 Bad Request: missing fields.
	if strings.Contains(errMsg, "required") {
		return http.StatusBadRequest
	}

	// Default: 500 Internal Server Error.
	return http.StatusInternalServerError
}

// CheckoutHandler handles HTTP requests for the Checkout Prepare and Commit APIs.
type CheckoutHandler struct {
	checkoutService *service.CheckoutService
}

// NewCheckoutHandler creates a new CheckoutHandler.
func NewCheckoutHandler(checkoutService *service.CheckoutService) *CheckoutHandler {
	return &CheckoutHandler{checkoutService: checkoutService}
}

// Prepare handles POST /api/v1/cli/checkout/prepare.
//
// Accepts a quote_id, wallet, and network. Validates the quote and generates
// a canonical signable order intent payload for the user wallet to sign.
//
// SECURITY FIX: F-001-01 — Supports X-Idempotency-Key header for idempotent prepare.
// If an idempotency key is provided and a matching prepared intent exists,
// the existing intent is returned (replay) instead of creating a duplicate.
func (h *CheckoutHandler) Prepare(c *gin.Context) {
	var req model.PrepareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "invalid request format",
		})
		return
	}

	// Validate required fields.
	var missing []string
	if req.QuoteID == "" {
		missing = append(missing, "quote_id")
	}
	if req.Wallet == "" {
		missing = append(missing, "wallet")
	}
	if req.Network == "" {
		missing = append(missing, "network")
	}
	if len(missing) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "required fields: " + strings.Join(missing, ", "),
		})
		return
	}

	// SECURITY FIX: F-001-01 — Extract optional idempotency key from header.
	if idempotencyKey := c.GetHeader("X-Idempotency-Key"); idempotencyKey != "" {
		req.IdempotencyKey = idempotencyKey
	}

	resp, err := h.checkoutService.PrepareCheckout(c.Request.Context(), &req)
	if err != nil {
		// SECURITY FIX: F-001-01 — Classify idempotency conflicts for prepare.
		errMsg := err.Error()
		if strings.Contains(errMsg, "idempotency key") && strings.Contains(errMsg, "different") {
			c.JSON(http.StatusConflict, gin.H{
				"code":    409,
				"message": errMsg,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// Commit handles POST /api/v1/cli/checkout/commit.
//
// Requires Idempotency-Key header. Validates the wallet signature (skeleton),
// marks the quote as consumed, and transitions the order intent to committed.
func (h *CheckoutHandler) Commit(c *gin.Context) {
	// Extract Idempotency-Key from header.
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "Idempotency-Key header is required",
		})
		return
	}

	var req model.CommitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "invalid request format",
		})
		return
	}

	// Validate required fields.
	var missing []string
	if req.OrderIntentID == "" {
		missing = append(missing, "order_intent_id")
	}
	if req.QuoteID == "" {
		missing = append(missing, "quote_id")
	}
	if req.Wallet == "" {
		missing = append(missing, "wallet")
	}
	if req.WalletSignature == "" {
		missing = append(missing, "wallet_signature")
	}
	if req.AgentSessionID == "" {
		missing = append(missing, "agent_session_id")
	}
	if len(missing) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "required fields: " + strings.Join(missing, ", "),
		})
		return
	}

	resp, err := h.checkoutService.CommitCheckout(c.Request.Context(), &req, idempotencyKey)
	if err != nil {
		errMsg := err.Error()
		// Classify the error to determine the appropriate HTTP status code.
		statusCode := classifyCommitError(errMsg)
		c.JSON(statusCode, gin.H{
			"code":    statusCode,
			"message": errMsg,
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}
