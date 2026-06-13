-- ============================================================================
-- ANCF Commerce - Initial Database Schema (Down Migration / Rollback)
-- Version: 001
-- Description: Drops all tables created by 001_init.sql in reverse dependency
--              order. WARNING: This destroys all data irreversibly.
-- ============================================================================

BEGIN;

DROP TABLE IF EXISTS outbox CASCADE;
DROP TABLE IF EXISTS audit_log CASCADE;
DROP TABLE IF EXISTS chain_txs CASCADE;
DROP TABLE IF EXISTS redemption_requests CASCADE;
DROP TABLE IF EXISTS mint_requests CASCADE;
DROP TABLE IF EXISTS ledger_entries CASCADE;
DROP TABLE IF EXISTS idempotency_keys CASCADE;
DROP TABLE IF EXISTS order_intents CASCADE;
DROP TABLE IF EXISTS quotes CASCADE;
DROP TABLE IF EXISTS catalog_skus CASCADE;
DROP TABLE IF EXISTS mint_policies CASCADE;
DROP TABLE IF EXISTS reserve_accounts CASCADE;
DROP TABLE IF EXISTS assets CASCADE;

DROP FUNCTION IF EXISTS catalog_skus_search_update() CASCADE;

COMMIT;
