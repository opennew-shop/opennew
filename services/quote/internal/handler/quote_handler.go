package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/quote/internal/model"
	"github.com/ancf-commerce/ancf/services/quote/internal/service"
)

// QuoteHandler handles HTTP requests for the Quote API.
type QuoteHandler struct {
	quoteService *service.QuoteService
}

// NewQuoteHandler creates a new QuoteHandler.
func NewQuoteHandler(quoteService *service.QuoteService) *QuoteHandler {
	return &QuoteHandler{quoteService: quoteService}
}

// GenerateQuote handles POST /api/v1/cli/quote.
//
// Accepts a wallet address, network, and line items (sku_id + quantity).
// Returns a server-authoritative price quote with a unique quote_id,
// per-line pricing, total, and expiration timestamp.
func (h *QuoteHandler) GenerateQuote(c *gin.Context) {
	var req model.QuoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "invalid request format",
		})
		return
	}

	// Validate required fields at the handler level for clear error messages.
	if req.Wallet == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "wallet is required",
		})
		return
	}
	if req.Network == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "network is required",
		})
		return
	}
	if len(req.Lines) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "at least one line item is required",
		})
		return
	}

	resp, err := h.quoteService.GenerateQuote(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}
