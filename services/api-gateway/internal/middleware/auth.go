package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/api-gateway/internal/config"
)

// whitelistedPaths contains paths that do not require authentication.
var whitelistedPaths = map[string]bool{
	"/health":                      true,
	"/.well-known/agent-rules.json": true,
}

// APIKeyAuth returns a Gin middleware that authenticates requests using:
//   - X-API-Key header (development mode: simple string comparison)
//   - Authorization: Bearer <JWT> header (production mode)
//
// Whitelisted paths (/health, /.well-known/agent-rules.json) are exempt.
func APIKeyAuth(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Skip authentication for whitelisted paths
		if whitelistedPaths[path] {
			c.Next()
			return
		}

		// Try X-API-Key header first (API Key auth)
		apiKey := c.GetHeader("X-API-Key")
		if apiKey != "" {
			if apiKey == cfg.APIKey {
				c.Set("auth_method", "api_key")
				c.Next()
				return
			}
			// If not a whitelisted API key, fall through to error
		}

		// Try Authorization: Bearer <token> header (JWT auth)
		// SECURITY FIX: F-003-01 — Removed dev-mode fallback that accepted any
		// non-empty Bearer token. Only tokens matching the configured JWT secret
		// are accepted. Failed verification returns 401.
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			if strings.HasPrefix(authHeader, "Bearer ") {
				token := strings.TrimPrefix(authHeader, "Bearer ")
				if token != "" {
					if token == cfg.JWTSecret {
						c.Set("auth_method", "jwt")
						c.Next()
						return
					}
					// Strict verification: no dev-mode fallback.
					// Token did not match the configured JWT secret.
				}
			}
		}

		// No valid authentication provided
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Missing or invalid authentication. Provide X-API-Key or Authorization: Bearer <token> header.",
				"request_id": c.GetString("request_id"),
			},
		})
		_ = c.Error(fmt.Errorf("missing or invalid authentication for %s %s", c.Request.Method, c.Request.URL.Path))
	}
}
