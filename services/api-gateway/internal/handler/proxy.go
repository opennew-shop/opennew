package handler

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ReverseProxy creates a Gin handler that forwards all requests to the given
// target base URL. The targetURL should be a complete URL including scheme, host,
// and optionally a path prefix (e.g. "http://127.0.0.1:8080/api/v1/cli/quote").
//
// The incoming request's query string is preserved and appended to the target.
// Request body, headers, and method are forwarded as-is. Response status, headers,
// and body are copied back to the client.
//
// Usage:
//
//	api.POST("/quote", handler.ReverseProxy(API_BASE + "/api/v1/cli/quote"))
//	api.GET("/wallet/balance", handler.ReverseProxy(API_BASE + "/api/v1/wallet/balance"))
func ReverseProxy(targetURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := targetURL
		if c.Request.URL.RawQuery != "" {
			if strings.Contains(target, "?") {
				target += "&" + c.Request.URL.RawQuery
			} else {
				target += "?" + c.Request.URL.RawQuery
			}
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":       500,
				"message":    "Failed to read request body",
				"request_id": c.GetString("request_id"),
			})
			return
		}
		// Restore body for downstream middleware
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		proxyReq, err := http.NewRequestWithContext(
			c.Request.Context(),
			c.Request.Method,
			target,
			bytes.NewReader(body),
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":       500,
				"message":    "Failed to create proxy request",
				"request_id": c.GetString("request_id"),
			})
			return
		}

		// Copy request headers (skip hop-by-hop headers)
		for key, values := range c.Request.Header {
			if isHopByHop(key) {
				continue
			}
			for _, value := range values {
				proxyReq.Header.Add(key, value)
			}
		}

		// Add X-Forwarded-For and X-Forwarded-Proto
		if clientIP := c.ClientIP(); clientIP != "" {
			proxyReq.Header.Set("X-Forwarded-For", clientIP)
		}
		proxyReq.Header.Set("X-Forwarded-Proto", "http")
		proxyReq.Header.Set("X-Forwarded-Host", c.Request.Host)

		client := &http.Client{
			Timeout: 30 * time.Second,
		}

		start := time.Now()
		resp, err := client.Do(proxyReq)
		duration := time.Since(start)

		if err != nil {
			slog.Warn("reverse proxy upstream unreachable",
				"target", target,
				"method", c.Request.Method,
				"error", err,
				"duration_ms", duration.Milliseconds(),
			)
			c.JSON(http.StatusBadGateway, gin.H{
				"code":       502,
				"message":    "Upstream service unavailable",
				"request_id": c.GetString("request_id"),
			})
			return
		}
		defer resp.Body.Close()

		// Copy response headers
		for key, values := range resp.Header {
			if isHopByHop(key) {
				continue
			}
			for _, value := range values {
				c.Header(key, value)
			}
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":       500,
				"message":    "Failed to read upstream response",
				"request_id": c.GetString("request_id"),
			})
			return
		}

		c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)

		slog.Debug("reverse proxy completed",
			"target", target,
			"status", resp.StatusCode,
			"duration_ms", duration.Milliseconds(),
		)
	}
}

// ProxyWithFallback creates a Gin handler that tries the primary serviceURL
// first. If the primary is unreachable (connection error or 5xx), it falls
// back to the secondary mockURL.
//
// This is intended for production mode where a real Go service should be used,
// but the mock server is available as a fail-safe during development.
//
// Usage:
//
//	api.POST("/quote", handler.ProxyWithFallback(
//	    "http://quote-service:8081/api/v1/cli/quote",
//	    "http://mock:8080/api/v1/cli/quote",
//	))
func ProxyWithFallback(serviceURL, mockURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":       500,
				"message":    "Failed to read request body",
				"request_id": c.GetString("request_id"),
			})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		targets := []string{serviceURL, mockURL}

		var lastErr error
		for i, target := range targets {
			targetWithQuery := target
			if c.Request.URL.RawQuery != "" {
				if strings.Contains(target, "?") {
					targetWithQuery += "&" + c.Request.URL.RawQuery
				} else {
					targetWithQuery += "?" + c.Request.URL.RawQuery
				}
			}

			proxyReq, reqErr := http.NewRequestWithContext(
				c.Request.Context(),
				c.Request.Method,
				targetWithQuery,
				bytes.NewReader(body),
			)
			if reqErr != nil {
				lastErr = reqErr
				continue
			}

			for key, values := range c.Request.Header {
				if isHopByHop(key) {
					continue
				}
				for _, value := range values {
					proxyReq.Header.Add(key, value)
				}
			}
			if clientIP := c.ClientIP(); clientIP != "" {
				proxyReq.Header.Set("X-Forwarded-For", clientIP)
			}
			proxyReq.Header.Set("X-Forwarded-Host", c.Request.Host)

			client := &http.Client{Timeout: 30 * time.Second}
			start := time.Now()
			resp, doErr := client.Do(proxyReq)
			duration := time.Since(start)

			if doErr != nil {
				lastErr = doErr
				slog.Warn("proxy fallback: primary failed, trying next",
					"attempt", i+1,
					"target", target,
					"error", doErr,
					"duration_ms", duration.Milliseconds(),
				)
				continue
			}

			if resp.StatusCode >= 500 {
				resp.Body.Close()
				lastErr = nil // not a network error, but an upstream error
				slog.Warn("proxy fallback: primary returned 5xx, trying next",
					"attempt", i+1,
					"target", target,
					"status", resp.StatusCode,
				)
				continue
			}

			// Success: copy response and return
			defer resp.Body.Close()
			for key, values := range resp.Header {
				if isHopByHop(key) {
					continue
				}
				for _, value := range values {
					c.Header(key, value)
				}
			}
			respBody, _ := io.ReadAll(resp.Body)
			c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)

			slog.Debug("proxy with fallback completed",
				"target", target,
				"attempt", i+1,
				"status", resp.StatusCode,
				"duration_ms", duration.Milliseconds(),
			)
			return
		}

		// All targets failed
		slog.Error("proxy fallback: all upstreams exhausted",
			"service", serviceURL,
			"mock", mockURL,
			"last_error", lastErr,
		)
		c.JSON(http.StatusBadGateway, gin.H{
			"code":       502,
			"message":    "All upstream services unavailable",
			"request_id": c.GetString("request_id"),
		})
	}
}

// isHopByHop returns true for HTTP headers that should not be forwarded by
// proxies (as defined in RFC 2616 section 13.5.1).
func isHopByHop(header string) bool {
	switch strings.ToLower(header) {
	case "connection", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailer",
		"transfer-encoding", "upgrade":
		return true
	}
	return false
}
