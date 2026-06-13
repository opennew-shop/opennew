package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// startTime records when the server started, used for uptime calculation.
var startTime = time.Now()

// HealthCheck returns a Gin handler for the GET /health endpoint.
// Returns service health status including version, uptime, and component checks.
// No authentication required.
func HealthCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		uptimeSeconds := int64(time.Since(startTime).Seconds())

		status := "ok"

		c.JSON(http.StatusOK, gin.H{
			"status":         status,
			"version":        "1.0.0",
			"uptime_seconds": uptimeSeconds,
			"timestamp":      time.Now().UTC().Format(time.RFC3339),
			"checks": gin.H{
				"gateway": "ok",
			},
		})
	}
}
