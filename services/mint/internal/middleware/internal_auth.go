// Package middleware 提供 mint 服务的 Gin 中间件。
// 当前包含服务间（service-to-service）写接口的内部 API Key 鉴权，
// 用于保护 /api/v1/internal 下的存款确认、赎回处理等变更类端点。
package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

// InternalAPIKeyHeader 是服务间内部接口用于传递鉴权密钥的 HTTP 头名称。
const InternalAPIKeyHeader = "X-Internal-API-Key"

// InternalAPIKeyAuth protects service-to-service mutation endpoints.
func InternalAPIKeyAuth(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if expected == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{
					"code":    "INTERNAL_AUTH_NOT_CONFIGURED",
					"message": "internal API authentication is not configured",
				},
			})
			return
		}

		got := c.GetHeader(InternalAPIKeyHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "UNAUTHORIZED_INTERNAL_REQUEST",
					"message": "missing or invalid internal API key",
				},
			})
			return
		}

		c.Next()
	}
}
