// Package repository 封装 catalog_skus 表的数据访问：全文检索、向量召回、混合检索（RRF 融合）、
// 行级锁与库存增减，以及商品的增删改查与嵌入向量维护。
package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ancf-commerce/ancf/services/catalog/internal/model"
)

// SKURepository provides data-access methods for the catalog_skus table.
type SKURepository struct {
	db *sql.DB
}

// NewSKURepository creates a new SKURepository with the given database connection.
func NewSKURepository(db *sql.DB) *SKURepository {
	return &SKURepository{db: db}
}

// Search performs a PostgreSQL full-text search against catalog_skus using
// the tsvector column search_vector. An empty query string returns all active SKUs.
//
// The search uses plainto_tsquery('english', $1) which converts the user input
// into a tsquery by normalizing words and combining them with & (AND).
// Results are ranked with ts_rank and ordered by relevance, then creation date.
func (r *SKURepository) Search(ctx context.Context, query string, limit, offset int) ([]model.SKU, int, error) {
	// Count total matching rows for pagination.
	countSQL := `SELECT COUNT(*)
FROM catalog_skus
WHERE status = 'active'
  AND ($1 = '' OR search_vector @@ plainto_tsquery('english', $1))`

	var total int
	if err := r.db.QueryRowContext(ctx, countSQL, query).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("sku_repository: count search results: %w", err)
	}

	// Fetch the page of results.
	selectSQL := `SELECT
    sku_id, title, description, currency, price_amount_minor, price_scale,
    stock, stock_hint, specs, media, status, created_at, updated_at,
    ts_rank(search_vector, plainto_tsquery('english', $1)) AS rank
FROM catalog_skus
WHERE status = 'active'
  AND ($1 = '' OR search_vector @@ plainto_tsquery('english', $1))
ORDER BY
    CASE WHEN $1 = '' THEN 0 ELSE 1 END,
    rank DESC,
    created_at DESC
LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, selectSQL, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("sku_repository: search query: %w", err)
	}
	defer rows.Close()

	var results []model.SKU
	for rows.Next() {
		var sku model.SKU
		var rank float64
		if err := rows.Scan(
			&sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
			&sku.PriceAmountMinor, &sku.PriceScale,
			&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
			&sku.Status, &sku.CreatedAt, &sku.UpdatedAt, &rank,
		); err != nil {
			return nil, 0, fmt.Errorf("sku_repository: scan row: %w", err)
		}
		results = append(results, sku)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("sku_repository: rows iteration: %w", err)
	}

	return results, total, nil
}

// GetBySKUID retrieves a single SKU by its sku_id.
// Returns nil, nil when no matching active SKU is found.
func (r *SKURepository) GetBySKUID(ctx context.Context, skuID string) (*model.SKU, error) {
	sql := `SELECT
    sku_id, title, description, currency, price_amount_minor, price_scale,
    stock, stock_hint, specs, media, status, created_at, updated_at
FROM catalog_skus
WHERE sku_id = $1 AND status = 'active'`

	var sku model.SKU
	err := r.db.QueryRowContext(ctx, sql, skuID).Scan(
		&sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
		&sku.PriceAmountMinor, &sku.PriceScale,
		&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
		&sku.Status, &sku.CreatedAt, &sku.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sku_repository: get by sku_id %s: %w", skuID, err)
	}
	return &sku, nil
}

// GetBySKUIDAny retrieves a single SKU by its sku_id regardless of status.
// Used by the product management API to view inactive/deleted products.
// Returns nil, nil when no matching SKU is found.
func (r *SKURepository) GetBySKUIDAny(ctx context.Context, skuID string) (*model.SKU, error) {
	sqlStmt := `SELECT
	    sku_id, title, description, currency, price_amount_minor, price_scale,
	    stock, stock_hint, specs, media, status, created_at, updated_at
	FROM catalog_skus
	WHERE sku_id = $1`

	var sku model.SKU
	err := r.db.QueryRowContext(ctx, sqlStmt, skuID).Scan(
		&sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
		&sku.PriceAmountMinor, &sku.PriceScale,
		&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
		&sku.Status, &sku.CreatedAt, &sku.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sku_repository: get any by sku_id %s: %w", skuID, err)
	}
	return &sku, nil
}

// LockSKUForUpdate locks a single SKU row for update within a transaction (SELECT FOR UPDATE).
// This prevents concurrent inventory deductions on the same SKU.
// Only locks active SKUs. Returns the SKU if found, or an error if not found.
func (r *SKURepository) LockSKUForUpdate(ctx context.Context, tx *sql.Tx, skuID string) (*model.SKU, error) {
	sqlStmt := `SELECT
		id, sku_id, title, description, currency, price_amount_minor, price_scale,
		stock, stock_hint, specs, media, status, created_at, updated_at
	FROM catalog_skus WHERE sku_id = $1 AND status = 'active'
	FOR UPDATE`

	var sku model.SKU
	err := tx.QueryRowContext(ctx, sqlStmt, skuID).Scan(
		&sku.ID, &sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
		&sku.PriceAmountMinor, &sku.PriceScale,
		&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
		&sku.Status, &sku.CreatedAt, &sku.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("sku %s not found or is not active", skuID)
	}
	if err != nil {
		return nil, fmt.Errorf("lock SKU %s: %w", skuID, err)
	}
	return &sku, nil
}

// DeductStockWithTx decrements stock for a given SKU within a transaction.
// Uses WHERE stock >= $2 to prevent negative inventory (no overselling).
// Returns an error if the SKU does not exist, is not active, or has insufficient stock.
func (r *SKURepository) DeductStockWithTx(ctx context.Context, tx *sql.Tx, skuID string, qty int) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE catalog_skus SET stock = stock - $2, updated_at = NOW()
		 WHERE sku_id = $1 AND stock >= $2 AND status = 'active'`, skuID, qty)
	if err != nil {
		return fmt.Errorf("deduct stock SKU %s: %w", skuID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("deduct stock rows affected SKU %s: %w", skuID, err)
	}
	if rows == 0 {
		return fmt.Errorf("insufficient stock for SKU %s", skuID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Agent Product Upload — CRUD operations
// ---------------------------------------------------------------------------

// CreateSKU inserts a new SKU row within a transaction.
// Returns the newly created SKU with its generated ID populated.
func (r *SKURepository) CreateSKU(ctx context.Context, tx *sql.Tx, sku *model.SKU) error {
	sqlStmt := `INSERT INTO catalog_skus
		(sku_id, title, description, currency, price_amount_minor, price_scale,
		 stock, stock_hint, specs, media, status, search_vector)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		setweight(to_tsvector('english', COALESCE($2, '')), 'A') ||
		setweight(to_tsvector('english', COALESCE($3, '')), 'B') ||
		setweight(to_tsvector('english', COALESCE($1, '')), 'C'))
	RETURNING id, created_at, updated_at`

	status := "active"
	if sku.Status != "" {
		status = sku.Status
	}
	desc := ""
	if sku.Description != nil {
		desc = *sku.Description
	}

	err := tx.QueryRowContext(ctx, sqlStmt,
		sku.SkuID, sku.Title, desc, sku.Currency,
		sku.PriceAmountMinor, sku.PriceScale,
		sku.Stock, sku.StockHint,
		sku.Specs, sku.Media, status,
	).Scan(&sku.ID, &sku.CreatedAt, &sku.UpdatedAt)
	if err != nil {
		return fmt.Errorf("sku_repository: create SKU %s: %w", sku.SkuID, err)
	}
	sku.Status = status
	return nil
}

// UpdateSKU updates an existing SKU row within a transaction.
// The caller provides a partially-populated SKU; only non-zero fields are updated.
func (r *SKURepository) UpdateSKU(ctx context.Context, tx *sql.Tx, skuID string, updates *model.SKU) error {
	sqlStmt := `UPDATE catalog_skus SET
		title = COALESCE(NULLIF($2, ''), title),
		description = COALESCE($3, description),
		currency = COALESCE(NULLIF($4, ''), currency),
		price_amount_minor = COALESCE(NULLIF($5, -1), price_amount_minor),
		price_scale = COALESCE(NULLIF($6, -1), price_scale),
		stock = COALESCE(NULLIF($7, -1), stock),
		specs = COALESCE($8, specs),
		media = COALESCE($9, media),
		status = COALESCE(NULLIF($10, ''), status),
		updated_at = NOW()
	WHERE sku_id = $1`

	var desc *string
	if updates.Description != nil {
		desc = updates.Description
	}
	var specs interface{}
	if len(updates.Specs) > 0 {
		specs = updates.Specs
	}
	var media interface{}
	if len(updates.Media) > 0 {
		media = updates.Media
	}

	sentinelNeg1 := int64(-1)
	priceVal := sentinelNeg1
	if updates.PriceAmountMinor != 0 {
		priceVal = updates.PriceAmountMinor
	}
	scaleVal := -1
	if updates.PriceScale != 0 {
		scaleVal = updates.PriceScale
	}
	stockVal := -1
	if updates.Stock != 0 {
		stockVal = updates.Stock
	}

	result, err := tx.ExecContext(ctx, sqlStmt,
		skuID, updates.Title, desc, updates.Currency,
		priceVal, scaleVal, stockVal, specs, media, updates.Status,
	)
	if err != nil {
		return fmt.Errorf("sku_repository: update SKU %s: %w", skuID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sku_repository: update SKU %s rows affected: %w", skuID, err)
	}
	if rows == 0 {
		return fmt.Errorf("sku_repository: SKU %s not found", skuID)
	}
	return nil
}

// DeleteSKU soft-deletes an SKU by setting its status to 'inactive'.
func (r *SKURepository) DeleteSKU(ctx context.Context, tx *sql.Tx, skuID string) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE catalog_skus SET status = 'inactive', updated_at = NOW() WHERE sku_id = $1`, skuID)
	if err != nil {
		return fmt.Errorf("sku_repository: delete SKU %s: %w", skuID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sku_repository: delete SKU %s rows affected: %w", skuID, err)
	}
	if rows == 0 {
		return fmt.Errorf("sku_repository: SKU %s not found", skuID)
	}
	return nil
}

// ListAll returns all SKUs (including inactive) with pagination, ordered by creation date descending.
func (r *SKURepository) ListAll(ctx context.Context, limit, offset int) ([]model.SKU, int, error) {
	countSQL := `SELECT COUNT(*) FROM catalog_skus`
	var total int
	if err := r.db.QueryRowContext(ctx, countSQL).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("sku_repository: count all: %w", err)
	}

	selectSQL := `SELECT
		sku_id, title, description, currency, price_amount_minor, price_scale,
		stock, stock_hint, specs, media, status, created_at, updated_at
	FROM catalog_skus
	ORDER BY created_at DESC
	LIMIT $1 OFFSET $2`

	rows, err := r.db.QueryContext(ctx, selectSQL, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("sku_repository: list all: %w", err)
	}
	defer rows.Close()

	var results []model.SKU
	for rows.Next() {
		var sku model.SKU
		if err := rows.Scan(
			&sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
			&sku.PriceAmountMinor, &sku.PriceScale,
			&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
			&sku.Status, &sku.CreatedAt, &sku.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("sku_repository: scan row in list all: %w", err)
		}
		results = append(results, sku)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("sku_repository: rows iteration in list all: %w", err)
	}
	return results, total, nil
}

// RestoreStockWithTx restores (increments) stock for a given SKU within a transaction.
// Used for refund or cancellation scenarios where previously deducted inventory must be returned.
func (r *SKURepository) RestoreStockWithTx(ctx context.Context, tx *sql.Tx, skuID string, qty int) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE catalog_skus SET stock = stock + $2, updated_at = NOW()
		 WHERE sku_id = $1`, skuID, qty)
	if err != nil {
		return fmt.Errorf("restore stock SKU %s: %w", skuID, err)
	}
	return nil
}

// List retrieves multiple SKUs by their sku_id values.
// Used by the quote service to validate and price individual line items.
// Only returns active SKUs.
func (r *SKURepository) List(ctx context.Context, skuIDs []string) ([]model.SKU, error) {
	if len(skuIDs) == 0 {
		return nil, nil
	}

	// Build a parameterized IN clause.
	query := `SELECT
    sku_id, title, description, currency, price_amount_minor, price_scale,
    stock, stock_hint, specs, media, status, created_at, updated_at
FROM catalog_skus
WHERE sku_id = ANY($1) AND status = 'active'`

	rows, err := r.db.QueryContext(ctx, query, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("sku_repository: list by sku_ids: %w", err)
	}
	defer rows.Close()

	var results []model.SKU
	for rows.Next() {
		var sku model.SKU
		if err := rows.Scan(
			&sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
			&sku.PriceAmountMinor, &sku.PriceScale,
			&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
			&sku.Status, &sku.CreatedAt, &sku.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("sku_repository: scan row in list: %w", err)
		}
		results = append(results, sku)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sku_repository: rows iteration in list: %w", err)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// pgvector + Embedding Operations (SUB-023)
// ---------------------------------------------------------------------------

// UpdateEmbedding sets the embedding vector for a SKU.
// The embedding parameter is serialized by model.Vector's driver.Valuer
// into the pgvector string format [0.1,0.2,...].
func (r *SKURepository) UpdateEmbedding(ctx context.Context, skuID string, embedding model.Vector) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE catalog_skus SET embedding = $2, updated_at = NOW()
		 WHERE sku_id = $1`, skuID, embedding)
	if err != nil {
		return fmt.Errorf("sku_repository: update embedding for %s: %w", skuID, err)
	}
	return nil
}

// SearchByVector performs a pure vector similarity search using the pgvector
// cosine distance operator (<=>). Results are ordered by ascending cosine
// distance, which is equivalent to descending cosine similarity.
//
// Only active SKUs with non-NULL embeddings are returned.
// The similarity score (1 - cosine_distance) is not returned here; use
// HybridSearch if you need the raw score.
func (r *SKURepository) SearchByVector(ctx context.Context, embedding model.Vector, limit int) ([]model.SKU, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `SELECT
	    id, sku_id, title, description, currency, price_amount_minor, price_scale,
	    stock, stock_hint, specs, media, status, embedding, created_at, updated_at
	FROM catalog_skus
	WHERE status = 'active' AND embedding IS NOT NULL
	ORDER BY embedding <=> $1
	LIMIT $2`

	rows, err := r.db.QueryContext(ctx, query, embedding, limit)
	if err != nil {
		return nil, fmt.Errorf("sku_repository: search by vector: %w", err)
	}
	defer rows.Close()

	var results []model.SKU
	for rows.Next() {
		var sku model.SKU
		if err := rows.Scan(
			&sku.ID, &sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
			&sku.PriceAmountMinor, &sku.PriceScale,
			&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
			&sku.Status, &sku.Embedding, &sku.CreatedAt, &sku.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("sku_repository: scan vector search row: %w", err)
		}
		results = append(results, sku)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sku_repository: vector search rows iteration: %w", err)
	}

	return results, nil
}

// HybridSearch performs a combined full-text search and vector similarity search
// using Reciprocal Rank Fusion (RRF).
//
// Algorithm:
//  1. Run FTS query against search_vector, get ranked results.
//  2. Run vector ANN query against embedding, get ranked results.
//  3. Fuse rankings with RRF: score = SUM(1 / (k + rank_i)) for each result.
//  4. Sort by fused score descending and return top-N.
//
// RRF constant k=60 is standard (reduces impact of high rankings by a single system).
// Results include the dominant search method that contributed to the fused score.
func (r *SKURepository) HybridSearch(ctx context.Context, query string, embedding model.Vector, limit int) ([]model.HybridSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	const rrfK = 60.0 // standard RRF constant

	// --- FTS leg ---
	ftsQuery := `SELECT
	    id, sku_id, title, description, currency, price_amount_minor, price_scale,
	    stock, stock_hint, specs, media, status, embedding, created_at, updated_at,
	    ts_rank(search_vector, plainto_tsquery('english', $1)) AS rank
	FROM catalog_skus
	WHERE status = 'active'
	  AND ($1 = '' OR search_vector @@ plainto_tsquery('english', $1))
	ORDER BY rank DESC
	LIMIT $2`

	ftsRows, err := r.db.QueryContext(ctx, ftsQuery, query, limit*3) // fetch more for fusion
	if err != nil {
		return nil, fmt.Errorf("sku_repository: hybrid FTS leg: %w", err)
	}

	type ftsResult struct {
		sku  model.SKU
		rank float64
	}
	var ftsResults []ftsResult
	for ftsRows.Next() {
		var sku model.SKU
		var rank float64
		if err := ftsRows.Scan(
			&sku.ID, &sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
			&sku.PriceAmountMinor, &sku.PriceScale,
			&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
			&sku.Status, &sku.Embedding, &sku.CreatedAt, &sku.UpdatedAt,
			&rank,
		); err != nil {
			ftsRows.Close()
			return nil, fmt.Errorf("sku_repository: scan FTS row: %w", err)
		}
		ftsResults = append(ftsResults, ftsResult{sku: sku, rank: rank})
	}
	ftsRows.Close()

	// --- Vector leg ---
	vectorQuery := `SELECT
	    id, sku_id, title, description, currency, price_amount_minor, price_scale,
	    stock, stock_hint, specs, media, status, embedding, created_at, updated_at,
	    embedding <=> $1 AS distance
	FROM catalog_skus
	WHERE status = 'active' AND embedding IS NOT NULL
	ORDER BY embedding <=> $1
	LIMIT $2`

	vecRows, err := r.db.QueryContext(ctx, vectorQuery, embedding, limit*3) // fetch more for fusion
	if err != nil {
		return nil, fmt.Errorf("sku_repository: hybrid vector leg: %w", err)
	}

	type vecResult struct {
		sku      model.SKU
		distance float64
	}
	var vecResults []vecResult
	for vecRows.Next() {
		var sku model.SKU
		var dist float64
		if err := vecRows.Scan(
			&sku.ID, &sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
			&sku.PriceAmountMinor, &sku.PriceScale,
			&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
			&sku.Status, &sku.Embedding, &sku.CreatedAt, &sku.UpdatedAt,
			&dist,
		); err != nil {
			vecRows.Close()
			return nil, fmt.Errorf("sku_repository: scan vector row: %w", err)
		}
		vecResults = append(vecResults, vecResult{sku: sku, distance: dist})
	}
	vecRows.Close()

	// --- RRF Fusion ---
	// Build score map: skuID -> {cumulative_score, method_hint}
	type rrfEntry struct {
		sku        model.SKU
		score      float64
		ftsRank    int // 0 = not in FTS results
		vectorRank int // 0 = not in vector results
	}
	rrfMap := make(map[string]*rrfEntry)

	for i, fr := range ftsResults {
		rank := i + 1
		entry, ok := rrfMap[fr.sku.SkuID]
		if !ok {
			entry = &rrfEntry{sku: fr.sku}
			rrfMap[fr.sku.SkuID] = entry
		}
		entry.score += 1.0 / (rrfK + float64(rank))
		entry.ftsRank = rank
	}

	for i, vr := range vecResults {
		rank := i + 1
		entry, ok := rrfMap[vr.sku.SkuID]
		if !ok {
			entry = &rrfEntry{sku: vr.sku}
			rrfMap[vr.sku.SkuID] = entry
		}
		entry.score += 1.0 / (rrfK + float64(rank))
		entry.vectorRank = rank
	}

	// Collect and sort by fused score descending.
	type scoredEntry struct {
		sku    model.SKU
		score  float64
		method string
	}
	var scored []scoredEntry
	for _, entry := range rrfMap {
		method := "fts"
		if entry.ftsRank == 0 && entry.vectorRank > 0 {
			method = "vector"
		} else if entry.ftsRank > 0 && entry.vectorRank > 0 {
			method = "hybrid"
		}
		scored = append(scored, scoredEntry{
			sku:    entry.sku,
			score:  entry.score,
			method: method,
		})
	}

	// Sort descending by score (insertion sort for small result sets).
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	// Truncate to limit.
	if len(scored) > limit {
		scored = scored[:limit]
	}

	results := make([]model.HybridSearchResult, len(scored))
	for i, se := range scored {
		results[i] = model.HybridSearchResult{
			SKU:        se.sku,
			Similarity: se.score,
			Method:     se.method,
		}
	}

	return results, nil
}
