package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ancf-commerce/ancf/services/catalog/internal/model"
)

// ErrSKULocked is returned when an SKU row is locked by a concurrent transaction.
// The caller should retry the operation (with exponential backoff).
var ErrSKULocked = fmt.Errorf("sku_repository: SKU is locked by another transaction")

// LockSKUForUpdateSkipLocked attempts to lock a single SKU row for update within
// a transaction using SELECT ... FOR UPDATE SKIP LOCKED.
//
// When multiple checkout transactions compete for the same SKU (e.g. flash sale),
// the first transaction that acquires the row lock proceeds, and subsequent
// callers receive ErrSKULocked immediately instead of blocking. This prevents
// cascading lock contention under high concurrency.
//
// Returns the locked SKU on success, or ErrSKULocked if the row is currently
// locked by another transaction.
//
// 中文说明：用 SELECT ... FOR UPDATE SKIP LOCKED 尝试锁定单个 SKU 行。
// 抢同一 SKU 时（如秒杀），先到者持锁推进，后到者立即收到 ErrSKULocked 而非阻塞，避免高并发锁竞争雪崩。
func (r *SKURepository) LockSKUForUpdateSkipLocked(ctx context.Context, tx *sql.Tx, skuID string) (*model.SKU, error) {
	sqlStmt := `SELECT
			id, sku_id, title, description, currency, price_amount_minor, price_scale,
			stock, stock_hint, specs, media, status, created_at, updated_at
		FROM catalog_skus WHERE sku_id = $1 AND status = 'active'
		FOR UPDATE SKIP LOCKED`

	var sku model.SKU
	err := tx.QueryRowContext(ctx, sqlStmt, skuID).Scan(
		&sku.ID, &sku.SkuID, &sku.Title, &sku.Description, &sku.Currency,
		&sku.PriceAmountMinor, &sku.PriceScale,
		&sku.Stock, &sku.StockHint, &sku.Specs, &sku.Media,
		&sku.Status, &sku.CreatedAt, &sku.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		// SKIP LOCKED means the row could exist but is locked, or it simply
		// doesn't exist. We treat both as "locked/unavailable" and let callers
		// retry. A separate existence check can distinguish the two cases.
		return nil, ErrSKULocked
	}
	if err != nil {
		return nil, fmt.Errorf("lock SKU %s with skip locked: %w", skuID, err)
	}
	return &sku, nil
}

// SKUItem represents a single item in a batch inventory operation.
type SKUItem struct {
	SKUID string
	Qty   int
}

// DeductStockBatch atomically deducts stock for multiple SKUs in a single SQL
// statement, reducing round-trips from N (one per SKU) to 1.
//
// Uses a CASE-WHEN expression to update each SKU to its new stock level,
// wrapped in a WHERE clause that ensures no SKU drops below zero.
//
// If any SKU has insufficient stock, the entire batch fails (no partial
// updates) and the caller should retry or abort.
func (r *SKURepository) DeductStockBatch(ctx context.Context, tx *sql.Tx, items []SKUItem) error {
	if len(items) == 0 {
		return nil
	}

	// Build CASE-WHEN expressions for batch update.
	// Example output:
	//   UPDATE catalog_skus SET stock = CASE sku_id
	//     WHEN 'sku_h100' THEN stock - 2
	//     WHEN 'sku_a100' THEN stock - 1
	//   END, updated_at = NOW()
	//   WHERE sku_id IN ('sku_h100', 'sku_a100')
	//     AND (sku_id = 'sku_h100' AND stock >= 2 OR sku_id = 'sku_a100' AND stock >= 1)
	var (
		caseBuilder   strings.Builder
		whereBuilder  strings.Builder
		skuIDs        = make([]string, len(items))
		args          = make([]interface{}, 0, len(items)*2)
		argIdx        = 1
	)

	caseBuilder.WriteString("UPDATE catalog_skus SET stock = CASE sku_id ")
	for i, item := range items {
		skuIDs[i] = item.SkuID
		caseBuilder.WriteString(fmt.Sprintf("WHEN $%d THEN stock - $%d ", argIdx, argIdx+1))
		if i > 0 {
			whereBuilder.WriteString(" OR ")
		}
		whereBuilder.WriteString(fmt.Sprintf("(sku_id = $%d AND stock >= $%d)", argIdx, argIdx+1))
		args = append(args, item.SkuID, item.Qty)
		argIdx += 2
	}
	caseBuilder.WriteString("END, updated_at = NOW()")

	// WHERE clause: sku_id IN (...) AND (stock >= required_qty for each)
	skuIDPlaceholders := make([]string, len(skuIDs))
	for i, skuID := range skuIDs {
		skuIDPlaceholders[i] = fmt.Sprintf("$%d", argIdx)
		args = append(args, skuID)
		argIdx++
	}

	fullSQL := fmt.Sprintf("%s WHERE sku_id IN (%s) AND status = 'active' AND (%s)",
		caseBuilder.String(),
		strings.Join(skuIDPlaceholders, ", "),
		whereBuilder.String(),
	)

	result, err := tx.ExecContext(ctx, fullSQL, args...)
	if err != nil {
		return fmt.Errorf("deduct batch: exec: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("deduct batch: rows affected: %w", err)
	}
	if int(rows) != len(items) {
		return fmt.Errorf("deduct batch: expected %d rows affected, got %d (insufficient stock for one or more SKUs)", len(items), rows)
	}

	return nil
}

// RestoreStockBatch atomically restores (increments) stock for multiple SKUs
// in a single SQL statement. Used for rollback or refund batch operations.
func (r *SKURepository) RestoreStockBatch(ctx context.Context, tx *sql.Tx, items []SKUItem) error {
	if len(items) == 0 {
		return nil
	}

	var caseBuilder strings.Builder
	skuIDs := make([]string, len(items))
	args := make([]interface{}, 0, len(items)*2)
	argIdx := 1

	caseBuilder.WriteString("UPDATE catalog_skus SET stock = CASE sku_id ")
	for i, item := range items {
		skuIDs[i] = item.SkuID
		caseBuilder.WriteString(fmt.Sprintf("WHEN $%d THEN stock + $%d ", argIdx, argIdx+1))
		args = append(args, item.SkuID, item.Qty)
		argIdx += 2
	}
	caseBuilder.WriteString("END, updated_at = NOW()")

	skuIDPlaceholders := make([]string, len(skuIDs))
	for i, skuID := range skuIDs {
		skuIDPlaceholders[i] = fmt.Sprintf("$%d", argIdx)
		args = append(args, skuID)
		argIdx++
	}

	fullSQL := fmt.Sprintf("%s WHERE sku_id IN (%s) AND status = 'active'",
		caseBuilder.String(),
		strings.Join(skuIDPlaceholders, ", "),
	)

	if _, err := tx.ExecContext(ctx, fullSQL, args...); err != nil {
		return fmt.Errorf("restore batch: %w", err)
	}
	return nil
}
