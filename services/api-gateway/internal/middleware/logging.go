package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// generateRequestID creates a short random request ID.
// 生成短随机请求 ID（crypto/rand 失败时回退为时间戳）。
func generateRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// fallback to timestamp if crypto/rand fails
		return time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(b)
}

// RequestLogger is a Gin middleware that logs each request with structured fields:
// request_id, method, path, status, duration, client_ip, user_agent.
func RequestLogger() gin.HandlerFunc {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return func(c *gin.Context) {
		// Determine request ID: use X-Request-ID header or generate one
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Set request ID in context and response headers
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)

		start := time.Now()
		path := c.Request.URL.Path
		rawQuery := c.Request.URL.RawQuery
		if rawQuery != "" {
			path = path + "?" + rawQuery
		}

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(start)
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		userAgent := c.Request.UserAgent()

		// Structured log entry
		attrs := []slog.Attr{
			slog.String("request_id", requestID),
			slog.String("method", method),
			slog.String("path", path),
			slog.Int("status", status),
			slog.String("duration", duration.String()),
			slog.String("client_ip", clientIP),
			slog.String("user_agent", userAgent),
		}

		// Log at appropriate level based on status code
		if status >= 500 {
			logger.LogAttrs(c.Request.Context(), slog.LevelError, "request completed", attrs...)
		} else if status >= 400 {
			logger.LogAttrs(c.Request.Context(), slog.LevelWarn, "request completed", attrs...)
		} else {
			logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "request completed", attrs...)
		}
	}
}
