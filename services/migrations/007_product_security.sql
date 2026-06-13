-- ============================================================================
-- ANCF Commerce - Product Data Security Isolation (Up Migration)
-- Version: 007
-- Task: SUB-038
-- Description: Adds product security columns to catalog_skus table.
--              The application layer validates wallet addresses and media URLs;
--              the database stores the security scan results for audit purposes.
--              Payment addresses MUST come from escrow, never product data.
-- ============================================================================

BEGIN;

-- ----------------------------------------------------------------------------
-- 1. Add security-scan columns to catalog_skus
--    App layer sets these after wallet/URL sanitization passes.
-- ----------------------------------------------------------------------------
ALTER TABLE catalog_skus
    ADD COLUMN IF NOT EXISTS security_scan_passed BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE catalog_skus
    ADD COLUMN IF NOT EXISTS security_scan_at TIMESTAMPTZ;

-- ----------------------------------------------------------------------------
-- 2. Add constraint to prevent agents from embedding wallet-like strings
--    in text fields. This is a defense-in-depth measure — the application
--    layer is the primary enforcement point.
--    The constraint uses a CHECK that matches common patterns:
--      - Ethereum/BSC/Polygon: 0x + 40 hex chars
--      - TRON base58: T + 33 alphanumeric chars
-- ----------------------------------------------------------------------------
ALTER TABLE catalog_skus
    ADD CONSTRAINT ck_product_no_evm_wallet_in_title
        CHECK (title !~ '0x[a-fA-F0-9]{40}');

ALTER TABLE catalog_skus
    ADD CONSTRAINT ck_product_no_evm_wallet_in_description
        CHECK (description IS NULL OR description !~ '0x[a-fA-F0-9]{40}');

ALTER TABLE catalog_skus
    ADD CONSTRAINT ck_product_no_tron_wallet_in_title
        CHECK (title !~ 'T[A-Za-z0-9]{33}');

ALTER TABLE catalog_skus
    ADD CONSTRAINT ck_product_no_tron_wallet_in_description
        CHECK (description IS NULL OR description !~ 'T[A-Za-z0-9]{33}');

-- ----------------------------------------------------------------------------
-- 3. Index on security_scan_passed for audit queries
-- ----------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_catalog_skus_security_scan
    ON catalog_skus (security_scan_passed, security_scan_at)
    WHERE security_scan_passed = false;

-- ----------------------------------------------------------------------------
-- 4. Audit log entry for products that fail security validation
--    (application layer responsibility; column serves as DB-side record)
-- ----------------------------------------------------------------------------
COMMENT ON COLUMN catalog_skus.security_scan_passed IS
    'Whether the product passed wallet-address and media-URL sanitization. Set by application layer on create/update.';

COMMENT ON COLUMN catalog_skus.security_scan_at IS
    'Timestamp of the most recent security scan for this product.';

COMMIT;
