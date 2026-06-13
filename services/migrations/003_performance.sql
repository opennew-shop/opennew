-- ============================================================================
-- ANCF Commerce - Performance Optimization Migration (Up Migration)
-- Version: 003
-- Description: Adds composite indexes for concurrent query patterns,
--              improves outbox polling, ledger balance aggregation,
--              and order intent state filtering.
-- ============================================================================

BEGIN;

-- ----------------------------------------------------------------------------
-- 1. outbox: composite partial index for pending event polling
--    The outbox processor queries WHERE status = 'pending' ORDER BY created_at
--    with FOR UPDATE SKIP LOCKED. This partial index covers that exact pattern.
-- ----------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_outbox_status_event_type
    ON outbox(status, event_type, created_at)
    WHERE status = 'pending';

-- ----------------------------------------------------------------------------
-- 2. outbox: index for stalled event recovery
--    The OutboxProcessorV2 recovery loop queries WHERE status = 'processing'
--    AND processed_at < NOW() - INTERVAL. This index speeds up that scan.
-- ----------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_outbox_processing_recovery
    ON outbox(status, processed_at)
    WHERE status = 'processing';

-- ----------------------------------------------------------------------------
-- 3. ledger_entries: composite index for wallet balance aggregation
--    GetBalance queries are the hottest read path in the system. The current
--    single-column index on (wallet) forces a full scan within the wallet to
--    evaluate CASE expressions for debit/credit/entry_type. This composite
--    index includes all columns needed for the balance query, enabling an
--    index-only scan.
-- ----------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_ledger_wallet_currency_entry
    ON ledger_entries(wallet, currency, entry_type, debit_account, credit_account);

-- ----------------------------------------------------------------------------
-- 4. order_intents: composite index for status-based listing with creation order
--    Admin dashboards and agent UIs frequently list intents by status sorted by
--    creation time. This index covers that query pattern.
-- ----------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_order_intents_status_created
    ON order_intents(status, created_at);

-- ----------------------------------------------------------------------------
-- 5. catalog_skus: partial index for active SKUs with stock > 0
--    Checkout inventory locking queries filter on sku_id AND stock >= qty AND
--    status = 'active'. The existing idx_catalog_skus_sku_id covers sku_id,
--    but this partial index gives the planner a better option for stock-aware
--    queries.
-- ----------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_catalog_skus_active_in_stock
    ON catalog_skus(sku_id, stock)
    WHERE status = 'active' AND stock > 0;

-- ----------------------------------------------------------------------------
-- 6. idempotency_keys: brin index for expire-at range queries
--    The cleanup job deletes rows WHERE expires_at < NOW(). A BRIN index is
--    extremely compact and efficient for monotonic timestamp range scans.
-- ----------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_brin
    ON idempotency_keys USING BRIN (expires_at);

-- ----------------------------------------------------------------------------
-- Rollback
-- ----------------------------------------------------------------------------
-- To revert this migration:
--
--   DROP INDEX IF EXISTS idx_outbox_status_event_type;
--   DROP INDEX IF EXISTS idx_outbox_processing_recovery;
--   DROP INDEX IF EXISTS idx_ledger_wallet_currency_entry;
--   DROP INDEX IF EXISTS idx_order_intents_status_created;
--   DROP INDEX IF EXISTS idx_catalog_skus_active_in_stock;
--   DROP INDEX IF EXISTS idx_idempotency_keys_expires_brin;

COMMIT;
