package handler

import (
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/ledger/internal/service"
)

// BalanceHandler handles HTTP requests for wallet balance queries.
type BalanceHandler struct {
	ledgerService *service.LedgerService
}

// NewBalanceHandler creates a new BalanceHandler.
func NewBalanceHandler(svc *service.LedgerService) *BalanceHandler {
	return &BalanceHandler{ledgerService: svc}
}

// GetBalance handles GET /api/v1/wallet/balance
//
// Query parameters:
//   - wallet (required): the wallet address
//   - currency (optional, default "vUSDC"): the currency symbol
//
// Response:
//
//	{
//	  "wallet": "USER_WALLET",
//	  "currency": "vUSDC",
//	  "available": 50000000,
//	  "pending": 1000000,
//	  "total_debit": 2500000,
//	  "total_credit": 52500000
//	}
func (h *BalanceHandler) GetBalance(c *gin.Context) {
	wallet := c.Query("wallet")
	if wallet == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "wallet query parameter is required",
			},
		})
		return
	}

	currency := c.DefaultQuery("currency", "vUSDC")

	balance, err := h.ledgerService.GetBalance(c.Request.Context(), wallet, currency)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "BALANCE_QUERY_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"wallet":       balance.Wallet,
		"currency":     balance.Currency,
		"available":    balance.Available,
		"pending":      balance.Pending,
		"total_debit":  balance.TotalDebit,
		"total_credit": balance.TotalCredit,
	})
}

// GetEntries handles GET /api/v1/wallet/entries
//
// Query parameters:
//   - wallet (required): the wallet address
//   - limit (optional, default 50, max 200): page size
//   - offset (optional, default 0): pagination offset
//
// Response:
//
//	{
//	  "entries": [ ... ],
//	  "limit": 50,
//	  "offset": 0
//	}
func (h *BalanceHandler) GetEntries(c *gin.Context) {
	wallet := c.Query("wallet")
	if wallet == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "wallet query parameter is required",
			},
		})
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}

	offset := 0
	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil {
			offset = parsed
		}
	}
	if offset < 0 {
		offset = 0
	}
	// Defensive cap
	if offset > math.MaxInt32/2 {
		offset = 0
	}

	entries, err := h.ledgerService.GetEntries(c.Request.Context(), wallet, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "ENTRIES_QUERY_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"entries": entries,
		"limit":   limit,
		"offset":  offset,
	})
}
