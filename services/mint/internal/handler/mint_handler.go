package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/mint/internal/model"
	"github.com/ancf-commerce/ancf/services/mint/internal/service"
)

// MintHandler handles HTTP requests for the mint service.
type MintHandler struct {
	service *service.MintService
}

// NewMintHandler creates a new MintHandler.
func NewMintHandler(svc *service.MintService) *MintHandler {
	return &MintHandler{service: svc}
}

// CreateDepositIntent handles POST /api/v1/wallet/deposit-intents
//
// Request body:
//
//	{
//	  "wallet": "USER_WALLET_ADDRESS",
//	  "network": "solana-mainnet",
//	  "asset_symbol": "vUSDC"
//	}
//
// Response:
//
//	{
//	  "deposit_intent_id": "di_abcdef...",
//	  "reserve_address": "RESERVE_WALLET_ADDRESS",
//	  "memo": "ancf-deposit:vUSDC:di_abcdef..."
//	}
func (h *MintHandler) CreateDepositIntent(c *gin.Context) {
	var req model.DepositIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_REQUEST",
				"message": "invalid request body: " + err.Error(),
			},
		})
		return
	}

	// Validate required fields.
	if req.Wallet == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "wallet is required",
			},
		})
		return
	}
	if req.Network == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "network is required",
			},
		})
		return
	}
	if req.AssetSymbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "asset_symbol is required",
			},
		})
		return
	}

	resp, err := h.service.CreateDepositIntent(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "DEPOSIT_INTENT_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"deposit_intent_id": resp.DepositIntentID,
		"reserve_address":   resp.ReserveAddress,
		"memo":              resp.Memo,
	})
}

// ConfirmDeposit handles POST /api/v1/wallet/deposit-confirm
//
// This endpoint is intended for internal use by the chain-adapter or admin.
// It confirms a deposit has been observed on-chain and triggers the mint/credit flow.
//
// Request body:
//
//	{
//	  "deposit_intent_id": "di_abcdef...",
//	  "deposit_tx_id": "5Hx...onchain_tx_signature",
//	  "amount_minor": 1000000
//	}
//
// Response:
//
//	{
//	  "request_id": "di_abcdef...",
//	  "status": "credited",
//	  "message": "deposit confirmed and credited"
//	}
func (h *MintHandler) ConfirmDeposit(c *gin.Context) {
	var req struct {
		DepositIntentID string `json:"deposit_intent_id" binding:"required"`
		DepositTxID     string `json:"deposit_tx_id" binding:"required"`
		AmountMinor     int64  `json:"amount_minor" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_REQUEST",
				"message": "invalid request body: " + err.Error(),
			},
		})
		return
	}

	if req.DepositIntentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "deposit_intent_id is required",
			},
		})
		return
	}
	if req.DepositTxID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "deposit_tx_id is required",
			},
		})
		return
	}
	if req.AmountMinor <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "amount_minor must be > 0",
			},
		})
		return
	}

	err := h.service.ConfirmDeposit(c.Request.Context(), req.DepositIntentID, req.DepositTxID, req.AmountMinor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "DEPOSIT_CONFIRM_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"request_id": req.DepositIntentID,
		"status":     model.MintStatusCredited,
		"message":    "deposit confirmed and credited",
	})
}

// GetMintStatus handles GET /api/v1/wallet/mint-status?request_id=xxx
//
// Query parameters:
//   - request_id (required): the deposit_intent_id / mint request_id
//
// Response:
//
//	{
//	  "request_id": "di_abcdef...",
//	  "wallet": "USER_WALLET",
//	  "asset_symbol": "vUSDC",
//	  "amount_minor": 1000000,
//	  "status": "credited",
//	  "created_at": "2026-06-04T12:00:00Z",
//	  "updated_at": "2026-06-04T12:01:00Z"
//	}
func (h *MintHandler) GetMintStatus(c *gin.Context) {
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

	resp, err := h.service.GetRequest(c.Request.Context(), requestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "MINT_STATUS_FAILED",
				"message": err.Error(),
			},
		})
		return
	}
	if resp == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"code":    "NOT_FOUND",
				"message": "mint request not found: " + requestID,
			},
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetReserveInfo handles GET /api/v1/wallet/reserve-info
//
// Query parameters:
//   - network (required): the network (e.g. "solana-mainnet")
//   - asset_symbol (required): the asset symbol (e.g. "vUSDC")
//
// Response:
//
//	{
//	  "network": "solana-mainnet",
//	  "asset_symbol": "vUSDC",
//	  "reserve_address": "RESERVE_WALLET...",
//	  "confirmed_balance_minor": 100000000,
//	  "pending_balance_minor": 5000000
//	}
func (h *MintHandler) GetReserveInfo(c *gin.Context) {
	network := c.Query("network")
	if network == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "network query parameter is required",
			},
		})
		return
	}

	assetSymbol := c.Query("asset_symbol")
	if assetSymbol == "" {
		assetSymbol = "vUSDC"
	}

	resp, err := h.service.GetReserveInfo(c.Request.Context(), network, assetSymbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "RESERVE_INFO_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}
