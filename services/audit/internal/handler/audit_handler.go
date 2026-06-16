// Package handler 实现审计服务的 HTTP 接口层，
// 提供管理端审计日志的查询、按 ID 获取、最近事件列表与手动记录端点。
package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/audit/internal/model"
	"github.com/ancf-commerce/ancf/services/audit/internal/service"
)

// AuditHandler handles HTTP requests for the audit service.
// All endpoints are under /api/v1/admin/audit and require admin role.
type AuditHandler struct {
	svc *service.AuditService
}

// NewAuditHandler creates a new AuditHandler.
func NewAuditHandler(svc *service.AuditService) *AuditHandler {
	return &AuditHandler{svc: svc}
}

// QueryAuditEvents handles GET /api/v1/admin/audit
//
// Query parameters (all optional):
//   - event_type    — filter by event_type (e.g. "mint_deposit_confirmed")
//   - actor_type    — filter by actor_type (user, agent, system, admin)
//   - actor_id      — filter by actor_id
//   - resource_type — filter by resource_type (order, quote, mint, redemption, …)
//   - resource_id   — filter by specific resource_id
//   - action        — filter by action (created, updated, deleted, …)
//   - from          — ISO8601 start timestamp (inclusive)
//   - to            — ISO8601 end timestamp (inclusive, defaults to now)
//   - limit         — max results (default 100, max 500)
//   - offset        — pagination offset (default 0)
//
// Response:
//
//	{
//	  "events": [ … ],
//	  "total": 1234,
//	  "limit": 100,
//	  "offset": 0
//	}
func (h *AuditHandler) QueryAuditEvents(c *gin.Context) {
	q := buildAuditQuery(c)

	events, total, err := h.svc.QueryEvents(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "AUDIT_QUERY_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	if events == nil {
		events = []*model.AuditEvent{}
	}

	c.JSON(http.StatusOK, gin.H{
		"events": events,
		"total":  total,
		"limit":  q.Limit,
		"offset": q.Offset,
	})
}

// GetAuditEvent handles GET /api/v1/admin/audit/:event_id
//
// Path parameter:
//   - event_id — the unique audit event identifier
//
// Response:
//
//	{
//	  "event": { … }
//	}
//
// Returns 404 if the event_id is not found.
func (h *AuditHandler) GetAuditEvent(c *gin.Context) {
	eventID := c.Param("event_id")
	if eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "event_id path parameter is required",
			},
		})
		return
	}

	event, err := h.svc.GetEvent(c.Request.Context(), eventID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "AUDIT_GET_FAILED",
				"message": err.Error(),
			},
		})
		return
	}
	if event == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"code":    "NOT_FOUND",
				"message": "audit event not found: " + eventID,
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"event": event,
	})
}

// GetRecentAuditEvents handles GET /api/v1/admin/audit/recent
//
// Query parameters:
//   - limit — max results (default 50, max 500)
//
// Response:
//
//	{
//	  "events": [ … ]
//	}
func (h *AuditHandler) GetRecentAuditEvents(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 500 {
		limit = 500
	}

	events, err := h.svc.GetRecent(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "AUDIT_RECENT_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	if events == nil {
		events = []*model.AuditEvent{}
	}

	c.JSON(http.StatusOK, gin.H{
		"events": events,
	})
}

// RecordAdminAuditEvent handles POST /api/v1/admin/audit
//
// Allows an admin to manually record an audit event (e.g. manual actions,
// policy changes, incident notes).
//
// Request body:
//
//	{
//	  "event_type": "manual_intervention",
//	  "actor_type": "admin",
//	  "actor_id": "admin@example.com",
//	  "resource_type": "mint",
//	  "resource_id": "di_abc123",
//	  "action": "manual_approval",
//	  "details": { "reason": "Large amount override", "ticket": "INC-1234" }
//	}
//
// Response (201):
//
//	{
//	  "event": { … }
//	}
func (h *AuditHandler) RecordAdminAuditEvent(c *gin.Context) {
	var req model.AdminAuditRequest
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
	if req.EventType == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "event_type is required",
			},
		})
		return
	}
	if req.ActorType == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "actor_type is required",
			},
		})
		return
	}
	if req.ResourceType == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "resource_type is required",
			},
		})
		return
	}
	if req.Action == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "action is required",
			},
		})
		return
	}

	// Only allow admin actor_type for admin-recorded events.
	if req.ActorType != model.ActorTypeAdmin {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "actor_type must be 'admin' for admin-recorded audit events",
			},
		})
		return
	}

	event, err := h.svc.RecordAdminEvent(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "AUDIT_RECORD_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"event": event,
	})
}

// buildAuditQuery constructs an AuditQuery from the request query parameters.
func buildAuditQuery(c *gin.Context) model.AuditQuery {
	q := model.AuditQuery{
		EventType:    c.Query("event_type"),
		ActorType:    c.Query("actor_type"),
		ActorID:      c.Query("actor_id"),
		ResourceType: c.Query("resource_type"),
		ResourceID:   c.Query("resource_id"),
		Action:       c.Query("action"),
	}

	if fromStr := c.Query("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			q.From = t
		}
	}
	if toStr := c.Query("to"); toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			q.To = t
		}
	}

	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			q.Limit = l
		}
	}
	if offsetStr := c.Query("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil {
			q.Offset = o
		}
	}

	q.Sanitize()
	return q
}
