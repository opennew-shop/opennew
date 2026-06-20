package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/catalog/service"
)

// CatalogSearchHandler exposes the catalog search endpoint from the API gateway.
// It wraps the catalog service's search handler and optionally supports hybrid search.
// 网关侧商品搜索处理器：包装 catalog 服务的搜索逻辑，可选支持混合检索(hybrid)。
type CatalogSearchHandler struct {
	searchHandler interface {
		Search(c *gin.Context)
		RagSearch(c *gin.Context)
	}
}

// NewCatalogSearchHandler creates a CatalogSearchHandler using the catalog's service layer.
// Accepts the catalog service directly, plus optional hybrid search components.
func NewCatalogSearchHandler(catalogSvc *service.CatalogService, hybridSvc *service.HybridSearchService, ragSvc *service.RAGService) gin.HandlerFunc {
	// Determine which handler to create based on available services.
	// We import and use the catalog handler package's handler.
	return func(c *gin.Context) {
		q := c.Query("q")
		modeStr := c.DefaultQuery("mode", "hybrid")
		mode := service.ParseSearchMode(modeStr)

		limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
		if err != nil {
			limit = 20
		}

		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if err != nil {
			offset = 0
		}

		if limit < 1 || limit > 100 {
			limit = 20
		}
		if offset < 0 {
			offset = 0
		}

		// Use hybrid search if available and requested.
		if hybridSvc != nil && mode != service.SearchModeKeyword {
			results, err := hybridSvc.Search(c.Request.Context(), q, limit, mode)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"code":       500,
					"message":    "Search service unavailable",
					"request_id": c.GetString("request_id"),
				})
				return
			}
			legacy := service.ToLegacyResult(results, len(results), limit, offset)
			c.JSON(http.StatusOK, legacy)
			return
		}

		// Fallback: legacy keyword-only search.
		result, err := catalogSvc.Search(c.Request.Context(), q, limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":       500,
				"message":    "Search service unavailable",
				"request_id": c.GetString("request_id"),
			})
			return
		}

		c.JSON(http.StatusOK, result)
	}
}

// NewRAGSearchHandler returns a handler for the Agent RAG search endpoint.
func NewRAGSearchHandler(ragSvc *service.RAGService, catalogSvc *service.CatalogService, hybridSvc *service.HybridSearchService) gin.HandlerFunc {
	return func(c *gin.Context) {
		q := c.Query("q")
		if q == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":       400,
				"message":    "Query parameter 'q' is required for RAG search",
				"request_id": c.GetString("request_id"),
			})
			return
		}

		mode := service.ParseSearchMode(c.DefaultQuery("mode", "hybrid"))

		topK, err := strconv.Atoi(c.DefaultQuery("top_k", "5"))
		if err != nil || topK < 1 || topK > 20 {
			topK = 5
		}

		if ragSvc != nil {
			resp, err := ragSvc.SearchForAgent(c.Request.Context(), q, topK, mode)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"code":       500,
					"message":    "RAG search unavailable",
					"request_id": c.GetString("request_id"),
				})
				return
			}
			c.JSON(http.StatusOK, resp)
			return
		}

		// Fallback: keyword search with simple context.
		result, err := catalogSvc.Search(c.Request.Context(), q, topK, 0)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":       500,
				"message":    "Search service unavailable",
				"request_id": c.GetString("request_id"),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"query":     q,
			"results":   result.Items,
			"context":   "RAG service unavailable. Using keyword search only.",
			"embedding": "unavailable",
			"mode":      "keyword",
			"top_k":     topK,
		})
	}
}
