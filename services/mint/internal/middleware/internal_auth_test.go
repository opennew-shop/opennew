package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestInternalAPIKeyAuth 验证内部 API Key 鉴权中间件在四种场景下的行为：
// 已配置且密钥匹配（放行 200）、已配置但缺失或错误（401）、以及未配置密钥（503）。
func TestInternalAPIKeyAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		expected   string
		header     string
		wantStatus int
	}{
		{name: "configured and valid", expected: "secret", header: "secret", wantStatus: http.StatusOK},
		{name: "configured and missing", expected: "secret", header: "", wantStatus: http.StatusUnauthorized},
		{name: "configured and invalid", expected: "secret", header: "wrong", wantStatus: http.StatusUnauthorized},
		{name: "not configured", expected: "", header: "secret", wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(InternalAPIKeyAuth(tt.expected))
			r.POST("/internal", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodPost, "/internal", nil)
			if tt.header != "" {
				req.Header.Set(InternalAPIKeyHeader, tt.header)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
