package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/catalog/internal/service"
)

// SearchHandler exposes the search endpoint for the catalog service.
type SearchHandler struct {
	catalogService  *service.CatalogService
	hybridService   *service.HybridSearchService
	ragService      *service.RAGService
}

// NewSearchHandler creates a new SearchHandler with keyword-only search.
func NewSearchHandler(catalogService *service.CatalogService) *SearchHandler {
	return &SearchHandler{catalogService: catalogService}
}

// NewHybridSearchHandler creates a new SearchHandler with hybrid search support.
func NewHybridSearchHandler(catalogService *service.CatalogService, hybridService *service.HybridSearchService, ragService *service.RAGService) *SearchHandler {
	return &SearchHandler{
		catalogService: catalogService,
		hybridService:  hybridService,
		ragService:     ragService,
	}
}

// Search godoc
//
//	@Summary		Search products
//	@Description	Performs a full-text search across active SKUs using PostgreSQL tsvector/tsquery.
//	@Description	Supports search modes: hybrid (RRF fusion), keyword (FTS), vector (semantic).
//	@Description	An empty query returns all active SKUs. Search results are display-only;
//	@Description	prices must be re-confirmed via the Quote API before checkout.
//	@Tags			catalog
//	@Param			q		query	string	false	"Search keyword"
//	@Param			mode	query	string	false	"Search mode: hybrid, keyword, vector"	default(hybrid)
//	@Param			limit	query	int		false	"Items per page"	default(20)	minimum(1)	maximum(100)
//	@Param			offset	query	int		false	"Page offset"	default(0)	minimum(0)
//	@Success		200		{object}	service.SearchResult
//	@Failure		500		{object}	map[string]interface{}
//	@Router			/api/v1/cli/search [get]
func (h *SearchHandler) Search(c *gin.Context) {
	q := c.Query("q")
	mode := service.ParseSearchMode(c.DefaultQuery("mode", "hybrid"))

	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil {
		limit = 20
	}

	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		offset = 0
	}

	// Parameter clamping is also done at the service layer for defense-in-depth.
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	// Route to hybrid search if available, otherwise fall back to keyword-only.
	if h.hybridService != nil && mode != service.SearchModeKeyword {
		results, err := h.hybridService.Search(c.Request.Context(), q, limit, mode)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":    500,
				"message": "Search service unavailable",
			})
			return
		}
		// Convert to legacy response format for backward compatibility.
		legacy := service.ToLegacyResult(results, len(results), limit, offset)
		c.JSON(http.StatusOK, legacy)
		return
	}

	// Fallback: legacy keyword-only search.
	result, err := h.catalogService.Search(c.Request.Context(), q, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "Search service unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// RagSearch performs an Agent-oriented RAG semantic search.
//
//	@Summary		RAG semantic search for Agents
//	@Description	Agent natural-language product discovery with hybrid search + context building.
//	@Tags			catalog,agent
//	@Param			q		query	string	true	"Natural language query"
//	@Param			mode	query	string	false	"Search mode: hybrid, keyword, vector"	default(hybrid)
//	@Param			top_k	query	int		false	"Number of results"	default(5)	minimum(1)	maximum(20)
//	@Success		200		{object}	service.AgentSearchResponse
//	@Failure		400		{object}	map[string]interface{}
//	@Failure		500		{object}	map[string]interface{}
//	@Router			/api/v1/cli/rag-search [get]
func (h *SearchHandler) RagSearch(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "Query parameter 'q' is required for RAG search",
		})
		return
	}

	mode := service.ParseSearchMode(c.DefaultQuery("mode", "hybrid"))

	topK, err := strconv.Atoi(c.DefaultQuery("top_k", "5"))
	if err != nil || topK < 1 || topK > 20 {
		topK = 5
	}

	// Fallback to keyword-only if hybrid/rag not configured.
	if h.ragService == nil {
		// Build simple context from keyword search.
		result, err := h.catalogService.Search(c.Request.Context(), q, topK, 0)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":    500,
				"message": "Search service unavailable",
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
		return
	}

	resp, err := h.ragService.SearchForAgent(c.Request.Context(), q, topK, mode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "RAG search unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}
