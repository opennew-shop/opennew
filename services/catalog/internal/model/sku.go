// Package model 定义商品目录服务的领域模型与数据传输对象：SKU、搜索结果、价格展示、
// 商品规格 / 媒体，以及兼容 pgvector 的向量类型 Vector。
package model

import (
	"encoding/json"
	"time"
)

// SKU represents a catalog stock-keeping unit (product) in the commerce system.
// It maps to the catalog_skus table in PostgreSQL.
type SKU struct {
	ID               int64           `json:"id" db:"id"`
	SkuID            string          `json:"sku_id" db:"sku_id"`
	Title            string          `json:"title" db:"title"`
	Description      *string         `json:"description,omitempty" db:"description"`
	Currency         string          `json:"currency" db:"currency"`
	PriceAmountMinor int64           `json:"price_amount_minor" db:"price_amount_minor"`
	PriceScale       int             `json:"price_scale" db:"price_scale"`
	Stock            int             `json:"stock" db:"stock"`
	StockHint        int             `json:"stock_hint" db:"stock_hint"`
	Specs            json.RawMessage `json:"specs" db:"specs"`
	Media            json.RawMessage `json:"media" db:"media"`
	Status           string          `json:"status" db:"status"`
	Embedding        *Vector         `json:"embedding,omitempty" db:"embedding"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}

// SKUSearchResult represents a display-only search result returned to clients.
// Search results are NOT usable for checkout pricing; clients must request a quote.
type SKUSearchResult struct {
	SkuID      string          `json:"sku_id"`
	Title      string          `json:"title"`
	Price      SKUPriceDisplay `json:"price"`
	StockHint  int             `json:"stock_hint"`
	Specs      json.RawMessage `json:"specs,omitempty"`
	Media      json.RawMessage `json:"media,omitempty"`
}

// SKUPriceDisplay is the price representation returned in search results.
type SKUPriceDisplay struct {
	Currency    string `json:"currency"`
	AmountMinor string `json:"amount_minor"`
	Scale       int    `json:"scale"`
}

// SKUSpecs represents the parsed GPU compute specifications from the specs JSONB field.
type SKUSpecs struct {
	GPU           string `json:"GPU,omitempty"`
	GPUMemory     string `json:"GPU_Memory,omitempty"`
	VCPU          int    `json:"vCPU,omitempty"`
	RAM           string `json:"RAM,omitempty"`
	Storage       string `json:"Storage,omitempty"`
	Network       string `json:"Network,omitempty"`
	CUDA          string `json:"CUDA,omitempty"`
	TFLOPSFP16    int    `json:"TFLOPS_FP16,omitempty"`
	Interconnect  string `json:"Interconnect,omitempty"`
	TensorCores   string `json:"Tensor_Cores,omitempty"`
}

// SKUMedia represents the media assets from the media JSONB field.
type SKUMedia struct {
	Thumbnail  string `json:"thumbnail,omitempty"`
	Banner     string `json:"banner,omitempty"`
	Datasheet  string `json:"datasheet,omitempty"`
}

// HybridSearchResult wraps an SKU with its similarity score and search method.
// Returned by the hybrid (FTS + vector) search with RRF fusion.
type HybridSearchResult struct {
	SKU        SKU     `json:"sku"`
	Similarity float64 `json:"similarity"`
	Method     string  `json:"method"` // "fts", "vector", "hybrid"
}

// ---------------------------------------------------------------------------
// Agent Product Upload DTOs
// ---------------------------------------------------------------------------

// CreateProductRequest is the request body for Agent-initiated product creation.
type CreateProductRequest struct {
	SkuID       string          `json:"sku_id,omitempty"`   // Optional, auto-generated if empty
	Title       string          `json:"title"`              // Required
	Description string          `json:"description,omitempty"`
	Currency    string          `json:"currency,omitempty"` // Default "vUSDC"
	PriceMinor  string          `json:"amount_minor"`       // Required, string for bigint safety
	PriceScale  int             `json:"scale,omitempty"`    // Default 6
	Stock       int             `json:"stock,omitempty"`
	Specs       json.RawMessage `json:"specs,omitempty"`
	Media       json.RawMessage `json:"media,omitempty"`
	AgentID     string          `json:"agent_id,omitempty"` // Creator identifier
}

// UpdateProductRequest is the request body for Agent-initiated product updates.
// All fields are optional — only provided fields are updated.
type UpdateProductRequest struct {
	Title       *string          `json:"title,omitempty"`
	Description *string          `json:"description,omitempty"`
	Currency    *string          `json:"currency,omitempty"`
	PriceMinor  *string          `json:"amount_minor,omitempty"` // string for bigint safety
	PriceScale  *int             `json:"scale,omitempty"`
	Stock       *int             `json:"stock,omitempty"`
	Specs       *json.RawMessage `json:"specs,omitempty"`
	Media       *json.RawMessage `json:"media,omitempty"`
	Status      *string          `json:"status,omitempty"` // active, inactive, discontinued
}
