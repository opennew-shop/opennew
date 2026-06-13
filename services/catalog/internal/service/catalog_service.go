package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ancf-commerce/ancf/services/catalog/internal/model"
	"github.com/ancf-commerce/ancf/services/catalog/internal/repository"
)

// CatalogService provides business logic for the product catalog.
type CatalogService struct {
	db   *sql.DB
	repo *repository.SKURepository
}

// NewCatalogService creates a new CatalogService.
func NewCatalogService(db *sql.DB, repo *repository.SKURepository) *CatalogService {
	return &CatalogService{db: db, repo: repo}
}

// SearchResult wraps a page of search results with pagination metadata.
type SearchResult struct {
	Items  []model.SKUSearchResult `json:"items"`
	Total  int                     `json:"total"`
	Limit  int                     `json:"limit"`
	Offset int                     `json:"offset"`
}

// Search performs a full-text search across active SKUs.
//
// Parameters are validated and clamped:
//   - limit defaults to 20, clamped to [1, 100]
//   - offset defaults to 0, clamped to [0, infinity)
//   - query may be empty to return all active SKUs
//
// Price amounts (int64 in the database) are converted to strings in the response
// to match the search-response.schema.json requirement (amount_minor as string).
func (s *CatalogService) Search(ctx context.Context, query string, limit, offset int) (*SearchResult, error) {
	// Validate and clamp parameters.
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	skus, total, err := s.repo.Search(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("catalog_service: search: %w", err)
	}

	// Convert domain model SKUs to display-only search results.
	items := make([]model.SKUSearchResult, 0, len(skus))
	for _, sku := range skus {
		items = append(items, model.SKUSearchResult{
			SkuID: sku.SkuID,
			Title: sku.Title,
			Price: model.SKUPriceDisplay{
				Currency:    sku.Currency,
				AmountMinor: strconv.FormatInt(sku.PriceAmountMinor, 10),
				Scale:       sku.PriceScale,
			},
			StockHint: sku.StockHint,
			Specs:     sku.Specs,
			Media:     sku.Media,
		})
	}

	return &SearchResult{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// GetBySKUID retrieves a single SKU by its identifier.
// Returns nil, nil if no matching active SKU exists.
// Used by the quote service to validate and price quote line items.
func (s *CatalogService) GetBySKUID(ctx context.Context, skuID string) (*model.SKU, error) {
	sku, err := s.repo.GetBySKUID(ctx, skuID)
	if err != nil {
		return nil, fmt.Errorf("catalog_service: get by sku_id: %w", err)
	}
	return sku, nil
}

// ListBySKUIDs retrieves multiple SKUs by their identifiers.
// Used by the quote service for bulk SKU validation and pricing.
func (s *CatalogService) ListBySKUIDs(ctx context.Context, skuIDs []string) ([]model.SKU, error) {
	skus, err := s.repo.List(ctx, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("catalog_service: list by sku_ids: %w", err)
	}
	return skus, nil
}

// ---------------------------------------------------------------------------
// Agent Product Upload — business logic
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// SUB-038: Product Data Security Isolation — Wallet Address Detection
// ---------------------------------------------------------------------------

// walletPatterns are compiled regular expressions that match common
// blockchain wallet addresses and private keys. They are used to prevent
// agents from embedding payment addresses in product data.
var walletPatterns = []*regexp.Regexp{
	regexp.MustCompile(`0x[a-fA-F0-9]{40}`),                    // Ethereum/BSC/Polygon address
	regexp.MustCompile(`[1-9A-HJ-NP-Za-km-z]{32,44}`),          // Solana base58 (loose)
	regexp.MustCompile(`T[A-Za-z0-9]{33}`),                      // TRON base58
	regexp.MustCompile(`0x[a-fA-F0-9]{64}`),                     // EVM private key
	regexp.MustCompile(`\[(\d{1,3},\s*){31}\d{1,3}\]`),         // Solana keypair byte array literal
}

// containsWalletAddress returns true if text matches any known wallet pattern.
func containsWalletAddress(text string) bool {
	for _, p := range walletPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// sanitizeWalletFields checks all text fields of a product for wallet addresses.
// Returns the list of field names that contain violations.
func sanitizeWalletFields(title, description string, specs json.RawMessage) []string {
	var violations []string

	if containsWalletAddress(title) {
		violations = append(violations, "title")
	}
	if description != "" && containsWalletAddress(description) {
		violations = append(violations, "description")
	}

	// Check specs JSON values
	if len(specs) > 0 && string(specs) != "{}" {
		var m map[string]interface{}
		if err := json.Unmarshal(specs, &m); err == nil {
			for k, v := range m {
				if s, ok := v.(string); ok && containsWalletAddress(s) {
					violations = append(violations, "specs."+k)
				}
			}
		}
	}

	return violations
}

// generateSKUID creates a deterministic SKU ID from an agent ID and title.
func generateSKUID(agentID, title string) string {
	h := sha256.Sum256([]byte(agentID + ":" + title))
	return "sku_" + fmt.Sprintf("%x", h)[:16]
}

// CreateProduct validates and creates a new product SKU.
//
// Validation:
//   - title is required
//   - amount_minor is required and must be a valid non-negative integer string
//   - If sku_id is not provided, one is auto-generated
//   - Currency defaults to "vUSDC", scale defaults to 6
func (s *CatalogService) CreateProduct(ctx context.Context, req *model.CreateProductRequest) (*model.SKU, error) {
	// Validate required fields.
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("catalog_service: title is required")
	}
	if strings.TrimSpace(req.PriceMinor) == "" {
		return nil, fmt.Errorf("catalog_service: amount_minor is required")
	}
	priceMinor, err := strconv.ParseInt(req.PriceMinor, 10, 64)
	if err != nil || priceMinor < 0 {
		return nil, fmt.Errorf("catalog_service: invalid amount_minor: %s", req.PriceMinor)
	}

	// SUB-038: Wallet address detection — reject products with embedded wallet addresses.
	specsRaw := req.Specs
	if len(specsRaw) == 0 {
		specsRaw = json.RawMessage("{}")
	}
	violations := sanitizeWalletFields(req.Title, req.Description, specsRaw)
	if len(violations) > 0 {
		return nil, fmt.Errorf("catalog_service: SECURITY: wallet address detected in product field(s): %s. Payment addresses must come from escrow system, not product data", strings.Join(violations, ", "))
	}

	// Defaults.
	currency := req.Currency
	if strings.TrimSpace(currency) == "" {
		currency = "vUSDC"
	}
	scale := req.PriceScale
	if scale <= 0 {
		scale = 6
	}

	// Generate SKU ID if not provided.
	skuID := strings.TrimSpace(req.SkuID)
	if skuID == "" {
		skuID = generateSKUID(req.AgentID, req.Title)
	}

	desc := req.Description
	var descPtr *string
	if strings.TrimSpace(desc) != "" {
		descPtr = &desc
	}

	specs := specsRaw // reuse the pre-validated specs
	media := req.Media
	if len(media) == 0 {
		media = json.RawMessage("{}")
	}

	sku := &model.SKU{
		SkuID:            skuID,
		Title:            req.Title,
		Description:      descPtr,
		Currency:         currency,
		PriceAmountMinor: priceMinor,
		PriceScale:       scale,
		Stock:            req.Stock,
		StockHint:        req.Stock,
		Specs:            specs,
		Media:            media,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("catalog_service: begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := s.repo.CreateSKU(ctx, tx, sku); err != nil {
		return nil, fmt.Errorf("catalog_service: create product: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("catalog_service: commit tx: %w", err)
	}

	return sku, nil
}

// UpdateProduct updates an existing product SKU.
// Only non-nil fields in the request are applied.
func (s *CatalogService) UpdateProduct(ctx context.Context, skuID string, req *model.UpdateProductRequest) error {
	if strings.TrimSpace(skuID) == "" {
		return fmt.Errorf("catalog_service: sku_id is required")
	}

	// SUB-038: Wallet address detection for update — reject changes that embed wallet addresses.
	var updateTitle, updateDesc string
	var updateSpecs json.RawMessage
	if req.Title != nil {
		updateTitle = *req.Title
	}
	if req.Description != nil {
		updateDesc = *req.Description
	}
	if req.Specs != nil {
		updateSpecs = *req.Specs
	}
	violations := sanitizeWalletFields(updateTitle, updateDesc, updateSpecs)
	if len(violations) > 0 {
		return fmt.Errorf("catalog_service: SECURITY: wallet address detected in product field(s): %s. Payment addresses must come from escrow system, not product data", strings.Join(violations, ", "))
	}

	// Build the SKU update from the request — only set fields that were provided.
	updates := &model.SKU{}

	if req.Title != nil {
		updates.Title = *req.Title
	}
	if req.Description != nil {
		updates.Description = req.Description
	}
	if req.Currency != nil {
		updates.Currency = *req.Currency
	}
	if req.PriceMinor != nil {
		priceMinor, err := strconv.ParseInt(*req.PriceMinor, 10, 64)
		if err != nil || priceMinor < 0 {
			return fmt.Errorf("catalog_service: invalid amount_minor: %s", *req.PriceMinor)
		}
		updates.PriceAmountMinor = priceMinor
	}
	if req.PriceScale != nil {
		updates.PriceScale = *req.PriceScale
	}
	if req.Stock != nil {
		if *req.Stock < 0 {
			return fmt.Errorf("catalog_service: stock must be >= 0")
		}
		updates.Stock = *req.Stock
	}
	if req.Specs != nil {
		updates.Specs = *req.Specs
	}
	if req.Media != nil {
		updates.Media = *req.Media
	}
	if req.Status != nil {
		updates.Status = *req.Status
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog_service: begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := s.repo.UpdateSKU(ctx, tx, skuID, updates); err != nil {
		return fmt.Errorf("catalog_service: update product: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("catalog_service: commit tx: %w", err)
	}

	return nil
}

// DeleteProduct soft-deletes a product by setting its status to 'inactive'.
func (s *CatalogService) DeleteProduct(ctx context.Context, skuID string) error {
	if strings.TrimSpace(skuID) == "" {
		return fmt.Errorf("catalog_service: sku_id is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog_service: begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := s.repo.DeleteSKU(ctx, tx, skuID); err != nil {
		return fmt.Errorf("catalog_service: delete product: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("catalog_service: commit tx: %w", err)
	}

	return nil
}

// GetBySKUIDAny retrieves a single SKU by its identifier regardless of status.
// Used by the product management API to view inactive/deleted products.
func (s *CatalogService) GetBySKUIDAny(ctx context.Context, skuID string) (*model.SKU, error) {
	sku, err := s.repo.GetBySKUIDAny(ctx, skuID)
	if err != nil {
		return nil, fmt.Errorf("catalog_service: get any by sku_id: %w", err)
	}
	return sku, nil
}

// ListProducts returns a paginated list of all products (including inactive).
func (s *CatalogService) ListProducts(ctx context.Context, limit, offset int) ([]model.SKU, int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListAll(ctx, limit, offset)
}
