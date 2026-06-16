package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/mint/internal/model"
	"github.com/ancf-commerce/ancf/services/mint/internal/service"
)

// RedemptionHandler handles HTTP requests for vUSDC redemption operations.
// vUSDC/AGP 赎回操作的 HTTP 处理器（8 状态赎回流程的接入层）。
type RedemptionHandler struct {
	redemptionService *service.RedemptionService
}

// NewRedemptionHandler creates a new RedemptionHandler.
func NewRedemptionHandler(svc *service.RedemptionService) *RedemptionHandler {
	return &RedemptionHandler{redemptionService: svc}
}

// ---------------------------------------------------------------------------
// POST /api/v1/wallet/redeem
// ---------------------------------------------------------------------------

// CreateRedemption handles the creation of a new redemption request.
//
// Request body (JSON):
//
//	{
//	  "wallet": "USER_WALLET",
//	  "network": "solana-mainnet",
//	  "asset_symbol": "vUSDC",
//	  "amount_minor": 10000000
//	}
//
// Response (201 Created):
//
//	{
//	  "request_id": "red_abc123...",
//	  "wallet": "USER_WALLET",
//	  "asset_symbol": "vUSDC",
//	  "amount_minor": 10000000,
//	  "status": "created",
//	  "created_at": "2026-06-07T08:00:00Z",
//	  "updated_at": "2026-06-07T08:00:00Z"
//	}
func (h *RedemptionHandler) CreateRedemption(c *gin.Context) {
	var req model.CreateRedemptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "wallet (string), network (string), asset_symbol (string), and amount_minor (int > 0) are required",
				"details": err.Error(),
			},
		})
		return
	}

	// Validate field constraints beyond gin binding
	if req.Wallet == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_PARAMETER", "message": "wallet is required"}})
		return
	}
	if req.Network == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_PARAMETER", "message": "network is required"}})
		return
	}
	if req.AssetSymbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_PARAMETER", "message": "asset_symbol is required"}})
		return
	}
	if req.AmountMinor <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_PARAMETER", "message": "amount_minor must be greater than 0"}})
		return
	}

	resp, err := h.redemptionService.CreateRedemption(c.Request.Context(), &req)
	if err != nil {
		// Distinguish between balance-related errors and internal errors
		errMsg := err.Error()
		// Simple heuristic: if the error mentions "insufficient balance", return 402
		// In production, use typed errors from the service layer.
		statusCode := http.StatusInternalServerError
		errorCode := "REDEMPTION_CREATE_FAILED"
		if len(errMsg) >= 20 && errMsg[:20] == "create_redemption: i" {
			statusCode = http.StatusPaymentRequired
			errorCode = "INSUFFICIENT_BALANCE"
		}

		c.JSON(statusCode, gin.H{
			"error": gin.H{
				"code":    errorCode,
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// ---------------------------------------------------------------------------
// POST /api/v1/internal/redeem/:request_id/process
// ---------------------------------------------------------------------------

// ProcessRedemption handles the redemption processing workflow.
// This is an internal endpoint used by the mint-service itself or an admin.
//
// Path parameter:
//   - request_id: the redemption request ID to process
//
// Response (200 OK):
//
//	{
//	  "request_id": "red_abc123...",
//	  "status": "payout_submitted",
//	  "message": "redemption processed successfully"
//	}
func (h *RedemptionHandler) ProcessRedemption(c *gin.Context) {
	requestID := c.Param("request_id")
	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "request_id path parameter is required",
			},
		})
		return
	}

	if err := h.redemptionService.ProcessRedemption(c.Request.Context(), requestID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "REDEMPTION_PROCESS_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"request_id": requestID,
		"status":     model.RedemptionStatusPayoutSubmitted,
		"message":    "redemption processed successfully",
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/wallet/redeem-status
// ---------------------------------------------------------------------------

// GetRedemptionStatus returns the current status of a redemption request.
//
// Query parameters:
//   - request_id (required): the redemption request ID
//
// Response (200 OK):
//
//	{
//	  "request_id": "red_abc123...",
//	  "wallet": "USER_WALLET",
//	  "asset_symbol": "vUSDC",
//	  "amount_minor": 10000000,
//	  "status": "payout_submitted",
//	  "burn_tx_id": "",
//	  "payout_tx_id": "",
//	  "created_at": "2026-06-07T08:00:00Z",
//	  "updated_at": "2026-06-07T08:00:05Z"
//	}
func (h *RedemptionHandler) GetRedemptionStatus(c *gin.Context) {
	requestID := c.Query("request_id")
	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "request_id query parameter is required",
			},
		})
		return
	}

	resp, err := h.redemptionService.GetRedemptionStatus(c.Request.Context(), requestID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"code":    "REDEMPTION_NOT_FOUND",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// POST /api/v1/internal/redeem/:request_id/payout
// ---------------------------------------------------------------------------

// CompletePayout marks the payout as completed.
// This endpoint is invoked by the chain-adapter or a manual admin confirmation.
//
// Path parameter:
//   - request_id: the redemption request ID
//
// Request body (JSON):
//
//	{
//	  "request_id": "red_abc123...",
//	  "payout_tx_id": "5xyz...on-chain-tx-hash"
//	}
//
// Response (200 OK):
//
//	{
//	  "request_id": "red_abc123...",
//	  "status": "paid",
//	  "message": "payout completed successfully"
//	}
func (h *RedemptionHandler) CompletePayout(c *gin.Context) {
	requestID := c.Param("request_id")
	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "request_id path parameter is required",
			},
		})
		return
	}

	var req model.PayoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "payout_tx_id is required",
				"details": err.Error(),
			},
		})
		return
	}
	if req.PayoutTxID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "payout_tx_id is required and must not be empty",
			},
		})
		return
	}

	if err := h.redemptionService.CompletePayout(c.Request.Context(), requestID, req.PayoutTxID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "PAYOUT_COMPLETE_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"request_id": requestID,
		"status":     model.RedemptionStatusPaid,
		"message":    "payout completed successfully",
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/internal/redeem/:request_id/release
// ---------------------------------------------------------------------------

// ReleaseFunds handles the release of locked funds for a failed redemption.
//
// Path parameter:
//   - request_id: the redemption request ID to release
//
// Response (200 OK):
//
//	{
//	  "request_id": "red_abc123...",
//	  "status": "released",
//	  "message": "funds released successfully"
//	}
func (h *RedemptionHandler) ReleaseFunds(c *gin.Context) {
	requestID := c.Param("request_id")
	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "request_id path parameter is required",
			},
		})
		return
	}

	if err := h.redemptionService.ReleaseFunds(c.Request.Context(), requestID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "RELEASE_FUNDS_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"request_id": requestID,
		"status":     model.RedemptionStatusReleased,
		"message":    "funds released successfully",
	})
}
