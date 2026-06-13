package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/catalog/internal/model"
	"github.com/ancf-commerce/ancf/services/catalog/internal/service"
)

// ProductHandler exposes Agent-initiated product CRUD endpoints.
type ProductHandler struct {
	catalogService *service.CatalogService
}

// NewProductHandler creates a new ProductHandler.
func NewProductHandler(catalogService *service.CatalogService) *ProductHandler {
	return &ProductHandler{catalogService: catalogService}
}

// ---------------------------------------------------------------------------
// POST /api/v1/catalog/products — Agent 创建商品
// ---------------------------------------------------------------------------

// CreateProduct godoc
//
//	@Summary		Create a product
//	@Description	Agent-initiated product creation. Title and amount_minor are required.
//	@Description	If sku_id is not provided, one is auto-generated from the agent_id and title.
//	@Tags			catalog-products
//	@Param			body	body	model.CreateProductRequest	true	"Product to create"
//	@Success		201		{object}	model.SKU
//	@Failure		400		{object}	map[string]interface{}
//	@Failure		500		{object}	map[string]interface{}
//	@Router			/api/v1/catalog/products [post]
func (h *ProductHandler) CreateProduct(c *gin.Context) {
	var req model.CreateProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "Invalid request body: " + err.Error(),
		})
		return
	}

	sku, err := h.catalogService.CreateProduct(c.Request.Context(), &req)
	if err != nil {
		code := http.StatusInternalServerError
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid") {
			code = http.StatusBadRequest
		}
		c.JSON(code, gin.H{
			"code":    code,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, sku)
}

// ---------------------------------------------------------------------------
// PUT /api/v1/catalog/products/:sku_id — Agent 更新商品
// ---------------------------------------------------------------------------

// UpdateProduct godoc
//
//	@Summary		Update a product
//	@Description	Agent-initiated product update. Only provided fields are modified.
//	@Tags			catalog-products
//	@Param			sku_id	path	string					true	"SKU identifier"
//	@Param			body	body	model.UpdateProductRequest	true	"Fields to update"
//	@Success		200		{object}	map[string]interface{}
//	@Failure		400		{object}	map[string]interface{}
//	@Failure		404		{object}	map[string]interface{}
//	@Failure		500		{object}	map[string]interface{}
//	@Router			/api/v1/catalog/products/{sku_id} [put]
func (h *ProductHandler) UpdateProduct(c *gin.Context) {
	skuID := c.Param("sku_id")
	if strings.TrimSpace(skuID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "sku_id path parameter is required",
		})
		return
	}

	var req model.UpdateProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "Invalid request body: " + err.Error(),
		})
		return
	}

	if err := h.catalogService.UpdateProduct(c.Request.Context(), skuID, &req); err != nil {
		code := http.StatusInternalServerError
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid") {
			code = http.StatusBadRequest
		}
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		c.JSON(code, gin.H{
			"code":    code,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"sku_id":  skuID,
		"status":  "updated",
		"message": "Product updated successfully",
	})
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/catalog/products/:sku_id — Agent 下架商品
// ---------------------------------------------------------------------------

// DeleteProduct godoc
//
//	@Summary		Delete (deactivate) a product
//	@Description	Agent-initiated product soft-delete. Sets status to 'inactive'.
//	@Tags			catalog-products
//	@Param			sku_id	path	string	true	"SKU identifier"
//	@Success		200		{object}	map[string]interface{}
//	@Failure		400		{object}	map[string]interface{}
//	@Failure		404		{object}	map[string]interface{}
//	@Failure		500		{object}	map[string]interface{}
//	@Router			/api/v1/catalog/products/{sku_id} [delete]
func (h *ProductHandler) DeleteProduct(c *gin.Context) {
	skuID := c.Param("sku_id")
	if strings.TrimSpace(skuID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "sku_id path parameter is required",
		})
		return
	}

	if err := h.catalogService.DeleteProduct(c.Request.Context(), skuID); err != nil {
		code := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		c.JSON(code, gin.H{
			"code":    code,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"sku_id":  skuID,
		"status":  "inactive",
		"message": "Product deactivated successfully",
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/catalog/products — Agent 商品列表
// ---------------------------------------------------------------------------

// ListProducts godoc
//
//	@Summary		List all products
//	@Description	Returns a paginated list of all products (including inactive).
//	@Tags			catalog-products
//	@Param			limit	query	int	false	"Items per page"	default(20)	minimum(1)	maximum(100)
//	@Param			offset	query	int	false	"Page offset"	default(0)	minimum(0)
//	@Success		200		{array}		model.SKU
//	@Failure		500		{object}	map[string]interface{}
//	@Router			/api/v1/catalog/products [get]
func (h *ProductHandler) ListProducts(c *gin.Context) {
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

	skus, total, err := h.catalogService.ListProducts(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "Failed to list products: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":  skus,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/catalog/products/:sku_id — 获取单个商品详情
// ---------------------------------------------------------------------------

// GetProduct godoc
//
//	@Summary		Get a single product
//	@Description	Returns a single product by SKU ID (including inactive).
//	@Tags			catalog-products
//	@Param			sku_id	path	string	true	"SKU identifier"
//	@Success		200		{object}	model.SKU
//	@Failure		404		{object}	map[string]interface{}
//	@Router			/api/v1/catalog/products/{sku_id} [get]
func (h *ProductHandler) GetProduct(c *gin.Context) {
	skuID := c.Param("sku_id")
	if strings.TrimSpace(skuID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "sku_id path parameter is required",
		})
		return
	}

	sku, err := h.catalogService.GetBySKUIDAny(c.Request.Context(), skuID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "Failed to retrieve product: " + err.Error(),
		})
		return
	}
	if sku == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "Product not found: " + skuID,
		})
		return
	}

	c.JSON(http.StatusOK, sku)
}
