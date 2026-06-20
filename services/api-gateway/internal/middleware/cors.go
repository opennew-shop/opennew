package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS returns a Gin middleware that allows cross-origin requests from
// Agent local renderer origins (127.0.0.1 and localhost).
// In development, it allows all local origins. In production, restrict to specific hosts.
// 中文说明：CORS 中间件，仅放行 Agent 本地渲染器来源(127.0.0.1/localhost)；生产环境应收紧到指定域名。
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// Allow local renderer origins
		allowed := isAllowedOrigin(origin)

		if allowed {
			// SECURITY FIX: F-003-02 — Only set ACAC:true for whitelisted origins.
			// Never echo back arbitrary origins.
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		// Non-whitelisted origins receive no CORS headers — the browser will
		// block the response, preventing cross-origin data leakage.
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-API-Key, X-Request-ID, Idempotency-Key, Signature, Signature-Input, Digest, Content-Digest")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID, X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset, Idempotency-Key")
		c.Header("Access-Control-Max-Age", "86400")

		// Content-Security-Policy for local Agent renderer
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src http://127.0.0.1:* http://localhost:*; img-src 'self' https://opennew.shop https://cdn.yourshop.com data:; font-src 'self'; object-src 'none'; base-uri 'self'; form-action 'none'")

		// Handle preflight
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// isAllowedOrigin checks if the origin is a local renderer origin.
func isAllowedOrigin(origin string) bool {
	if origin == "" {
		return true // same-origin requests have no Origin header
	}

	allowedPrefixes := []string{
		"http://127.0.0.1",
		"http://localhost",
		"https://localhost",
		"http://[::1]",
	}

	for _, prefix := range allowedPrefixes {
		if len(origin) >= len(prefix) && origin[:len(prefix)] == prefix {
			return true
		}
	}

	return false
}
