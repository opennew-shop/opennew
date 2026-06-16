package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/mint/internal/service"
)

// ReconciliationHandler handles HTTP requests for reserve-vs-liability
// reconciliation operations.
// 储备对账操作的 HTTP 处理器（接入 ReconciliationService）。
type ReconciliationHandler struct {
	reconciliationService *service.ReconciliationService
}

// NewReconciliationHandler creates a new ReconciliationHandler.
func NewReconciliationHandler(svc *service.ReconciliationService) *ReconciliationHandler {
	return &ReconciliationHandler{reconciliationService: svc}
}

// TriggerReconciliation handles POST /api/v1/admin/reconcile
//
// Manually triggers a reserve reconciliation for the specified asset.
//
// Request body (JSON):
//
//	{
//	  "asset_symbol": "vUSDC"
//	}
//
// Response (200 OK):
//
//	{
//	  "asset_symbol": "vUSDC",
//	  "reserve_confirmed_balance_minor": 100000000000,
//	  "internal_liability_minor": 85000000000,
//	  "pending_redemption_minor": 5000000000,
//	  "difference_minor": 10000000000,
//	  "is_balanced": true,
//	  "reconciled_at": "2026-06-07T12:00:00Z"
//	}
//
// If an alert is generated (difference < 0), the alert_message field is included.
func (h *ReconciliationHandler) TriggerReconciliation(c *gin.Context) {
	var req struct {
		AssetSymbol string `json:"asset_symbol"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		// If the body is empty or missing, default to vUSDC.
		req.AssetSymbol = "vUSDC"
	}

	if req.AssetSymbol == "" {
		req.AssetSymbol = "vUSDC"
	}

	result, err := h.reconciliationService.Reconcile(c.Request.Context(), req.AssetSymbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "RECONCILIATION_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetReconciliationStatus handles GET /api/v1/admin/reconciliation-status
//
// Returns the most recent reconciliation result from the in-memory cache.
// Returns 404 if no reconciliation has been performed yet.
//
// Response (200 OK): same shape as TriggerReconciliation response.
func (h *ReconciliationHandler) GetReconciliationStatus(c *gin.Context) {
	result := h.reconciliationService.GetLastResult()
	if result == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"code":    "NO_RECONCILIATION_DATA",
				"message": "no reconciliation has been performed yet",
			},
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// TriggerDailyReconciliation handles POST /api/v1/admin/reconcile/daily
//
// Runs reconciliation for all active assets. Designed for cron / scheduled
// invocation.
//
// Response (200 OK): array of reconciliation results.
func (h *ReconciliationHandler) TriggerDailyReconciliation(c *gin.Context) {
	results, err := h.reconciliationService.DailyReconciliation(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "DAILY_RECONCILIATION_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"assets":    len(results),
		"results":   results,
		"timestamp": results[0].ReconciledAt, // works because ReconcileAt is set on every entry
	})
}
