// Package handler 实现服务开通的 HTTP 接口层，
// 提供管理端手动开通与状态查询，以及面向用户 (Agent) 的访问凭据获取端点。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/provisioning/internal/model"
	"github.com/ancf-commerce/ancf/services/provisioning/internal/service"
)

// ProvisioningHandler handles HTTP requests for the provisioning service.
type ProvisioningHandler struct {
	provisioningService *service.ProvisioningService
}

// NewProvisioningHandler creates a new ProvisioningHandler.
func NewProvisioningHandler(provisioningService *service.ProvisioningService) *ProvisioningHandler {
	return &ProvisioningHandler{provisioningService: provisioningService}
}

// ManualProvision handles POST /api/v1/admin/provision/:intent_id.
//
// Manually triggers service provisioning for a given order intent.
// This is an admin endpoint for fallback or retry scenarios when the
// automated outbox-driven provisioning is not sufficient.
func (h *ProvisioningHandler) ManualProvision(c *gin.Context) {
	intentID := c.Param("intent_id")
	if intentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "intent_id path parameter is required",
		})
		return
	}

	// Optional request body for additional parameters (future extensibility).
	var req model.ManualProvisionRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			// Not a fatal error; use the path parameter.
		}
	}
	if req.OrderIntentID == "" {
		req.OrderIntentID = intentID
	}

	result, err := h.provisioningService.ManualProvision(c.Request.Context(), req.OrderIntentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	statusCode := http.StatusOK
	if result.Status == model.ProvStatusFailed {
		statusCode = http.StatusUnprocessableEntity
	}

	c.JSON(statusCode, gin.H{
		"code":    statusCode,
		"message": "provisioning completed",
		"data":    result,
	})
}

// GetProvisioningStatus handles GET /api/v1/admin/provision-status/:intent_id.
//
// Returns the current provisioning status for a given order intent.
// This is an admin endpoint for diagnostics and monitoring.
func (h *ProvisioningHandler) GetProvisioningStatus(c *gin.Context) {
	intentID := c.Param("intent_id")
	if intentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "intent_id path parameter is required",
		})
		return
	}

	status, err := h.provisioningService.GetProvisioningStatus(c.Request.Context(), intentID)
	if err != nil {
		// Distinguish between not-found and internal errors.
		if err.Error() == "get provisioning status: intent "+intentID+" not found" {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    404,
				"message": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": status,
	})
}

// GetProvisionAccess handles GET /api/v1/cli/provision-access/:intent_id.
//
// Returns the access credentials (access_token, instance_id, endpoint_url)
// for a successfully provisioned service. This is the user-facing endpoint
// called by the Agent after checkout to obtain service access.
//
// Only returns credentials if the order status is 'completed'.
// Returns appropriate status codes for in-progress or failed provisioning.
func (h *ProvisioningHandler) GetProvisionAccess(c *gin.Context) {
	intentID := c.Param("intent_id")
	if intentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "intent_id path parameter is required",
		})
		return
	}

	access, err := h.provisioningService.GetProvisionAccess(c.Request.Context(), intentID)
	if err != nil {
		// Distinguish between not-found and internal errors.
		errMsg := err.Error()
		if errMsg == "get provision access: intent "+intentID+" not found" {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    404,
				"message": errMsg,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": errMsg,
		})
		return
	}

	// Map order status to HTTP response.
	switch access.Status {
	case "completed":
		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"data": access,
		})
	case "provisioning":
		c.JSON(http.StatusAccepted, gin.H{
			"code":    202,
			"message": "provisioning in progress",
			"data":    access,
		})
	case "failed":
		c.JSON(http.StatusGone, gin.H{
			"code":    410,
			"message": "provisioning failed",
			"data":    access,
		})
	default:
		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"data": access,
		})
	}
}
