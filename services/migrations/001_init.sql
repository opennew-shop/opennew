-- ============================================================================
-- ANCF Commerce - Initial Database Schema (Up Migration)
-- Version: 001
-- Description: Creates all core tables for the ANCF Zero-Frontend Commerce
--              platform including catalog, quote, order, ledger, mint,
--              chain transactions, audit, and outbox.
-- ============================================================================

BEGIN;

-- ----------------------------------------------------------------------------
-- 1. assets - Asset definitions (shadow-ledger, SPL token, Token-2022)
-- ----------------------------------------------------------------------------
CREATE TABLE assets (
    id BIGSERIAL PRIMARY KEY,
    symbol VARCHAR(20) NOT NULL,
    decimals INTEGER NOT NULL DEFAULT 6,
    asset_type VARCHAR(50) NOT NULL CHECK (asset_type IN ('shadow-ledger', 'spl-token', 'token-2022')),
    network VARCHAR(50) NOT NULL,
    mint_address VARCHAR(88),
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'paused')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_assets_symbol ON assets(symbol);
CREATE INDEX idx_assets_type_network ON assets(asset_type, network);

-- ----------------------------------------------------------------------------
-- 2. reserve_accounts - Reserve account tracking for each network/asset
-- ----------------------------------------------------------------------------
CREATE TABLE reserve_accounts (
    id BIGSERIAL PRIMARY KEY,
    network VARCHAR(50) NOT NULL,
    asset_symbol VARCHAR(20) NOT NULL,
    address VARCHAR(88) NOT NULL,
    confirmed_balance_minor BIGINT NOT NULL DEFAULT 0
        CHECK (confirmed_balance_minor >= 0),
    pending_balance_minor BIGINT NOT NULL DEFAULT 0
        CHECK (pending_balance_minor >= 0),
    last_reconciled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (network, asset_symbol)
);

CREATE INDEX idx_reserve_accounts_network ON reserve_accounts(network);

-- ----------------------------------------------------------------------------
-- 3. mint_policies - Minting policy rules per asset
-- ----------------------------------------------------------------------------
CREATE TABLE mint_policies (
    id BIGSERIAL PRIMARY KEY,
    asset_id BIGINT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    daily_mint_limit_minor BIGINT NOT NULL CHECK (daily_mint_limit_minor >= 0),
    per_wallet_limit_minor BIGINT NOT NULL CHECK (per_wallet_limit_minor >= 0),
    require_manual_approval_above_minor BIGINT NOT NULL DEFAULT 0
        CHECK (require_manual_approval_above_minor >= 0),
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_mint_policies_asset ON mint_policies(asset_id);

-- ----------------------------------------------------------------------------
-- 4. catalog_skus - Product SKU catalog with JSONB specs and media
-- ----------------------------------------------------------------------------
CREATE TABLE catalog_skus (
    id BIGSERIAL PRIMARY KEY,
    sku_id VARCHAR(100) NOT NULL UNIQUE,
    title VARCHAR(500) NOT NULL,
    description TEXT,
    currency VARCHAR(20) NOT NULL DEFAULT 'vUSDC',
    price_amount_minor BIGINT NOT NULL CHECK (price_amount_minor >= 0),
    price_scale INTEGER NOT NULL DEFAULT 6,
    stock INTEGER NOT NULL DEFAULT 0 CHECK (stock >= 0),
    stock_hint INTEGER NOT NULL DEFAULT 0,
    specs JSONB DEFAULT '{}',
    media JSONB DEFAULT '{}',
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'discontinued')),
    search_vector TSVECTOR,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_catalog_skus_sku_id ON catalog_skus(sku_id);
CREATE INDEX idx_catalog_skus_search ON catalog_skus USING GIN(search_vector);
CREATE INDEX idx_catalog_skus_status ON catalog_skus(status) WHERE status = 'active';
CREATE INDEX idx_catalog_skus_price ON catalog_skus(price_amount_minor);

-- Trigger to update search_vector on insert/update
CREATE OR REPLACE FUNCTION catalog_skus_search_update() RETURNS TRIGGER AS $$
BEGIN
    NEW.search_vector :=
        setweight(to_tsvector('english', COALESCE(NEW.title, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.description, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.sku_id, '')), 'C');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_catalog_skus_search
    BEFORE INSERT OR UPDATE ON catalog_skus
    FOR EACH ROW EXECUTE FUNCTION catalog_skus_search_update();

-- ----------------------------------------------------------------------------
-- 5. quotes - Price quotes with expiration and consumption tracking
-- ----------------------------------------------------------------------------
CREATE TABLE quotes (
    id BIGSERIAL PRIMARY KEY,
    quote_id VARCHAR(100) NOT NULL UNIQUE,
    wallet VARCHAR(88) NOT NULL,
    network VARCHAR(50) NOT NULL,
    currency VARCHAR(20) NOT NULL DEFAULT 'vUSDC',
    total_minor BIGINT NOT NULL CHECK (total_minor >= 0),
    scale INTEGER NOT NULL DEFAULT 6,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed BOOLEAN NOT NULL DEFAULT FALSE,
    consumed_at TIMESTAMPTZ,
    lines JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_quotes_quote_id ON quotes(quote_id);
CREATE INDEX idx_quotes_wallet ON quotes(wallet);
CREATE INDEX idx_quotes_expires ON quotes(expires_at) WHERE consumed = FALSE;
CREATE INDEX idx_quotes_consumed ON quotes(consumed);

-- ----------------------------------------------------------------------------
-- 6. order_intents - Order intention with idempotency and signature support
-- ----------------------------------------------------------------------------
CREATE TABLE order_intents (
    id BIGSERIAL PRIMARY KEY,
    intent_id VARCHAR(100) NOT NULL UNIQUE,
    quote_id VARCHAR(100) NOT NULL REFERENCES quotes(quote_id) ON DELETE RESTRICT,
    wallet VARCHAR(88) NOT NULL,
    network VARCHAR(50) NOT NULL,
    currency VARCHAR(20) NOT NULL DEFAULT 'vUSDC',
    total_minor BIGINT NOT NULL CHECK (total_minor >= 0),
    status VARCHAR(30) NOT NULL DEFAULT 'created'
        CHECK (status IN (
            'created', 'prepared', 'committed', 'paid',
            'provisioning', 'completed', 'failed', 'refunded'
        )),
    idempotency_key VARCHAR(200),
    wallet_signature TEXT,
    agent_session_id VARCHAR(200),
    nonce VARCHAR(64),
    signable_payload JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_order_intents_intent_id ON order_intents(intent_id);
CREATE INDEX idx_order_intents_quote_id ON order_intents(quote_id);
CREATE INDEX idx_order_intents_wallet ON order_intents(wallet);
CREATE INDEX idx_order_intents_status ON order_intents(status);
CREATE INDEX idx_order_intents_idempotency ON order_intents(idempotency_key);

-- ----------------------------------------------------------------------------
-- 7. idempotency_keys - Idempotency key registry for all mutation APIs
-- ----------------------------------------------------------------------------
CREATE TABLE idempotency_keys (
    id BIGSERIAL PRIMARY KEY,
    key VARCHAR(200) NOT NULL UNIQUE,
    request_body_hash VARCHAR(128) NOT NULL,
    response_body TEXT,
    status_code INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE INDEX idx_idempotency_keys_expires ON idempotency_keys(expires_at);

-- ----------------------------------------------------------------------------
-- 8. ledger_entries - Double-entry accounting ledger (immutable)
-- ----------------------------------------------------------------------------
CREATE TABLE ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    entry_id VARCHAR(100) NOT NULL UNIQUE,
    transaction_id VARCHAR(100) NOT NULL,
    wallet VARCHAR(88),
    debit_account VARCHAR(50) NOT NULL
        CHECK (debit_account IN (
            'user_available', 'user_pending', 'merchant_pending',
            'merchant_settled', 'platform_fee', 'reserve_liability',
            'redemption_pending', 'mint_pending', 'reserve_asset'
        )),
    credit_account VARCHAR(50) NOT NULL
        CHECK (credit_account IN (
            'user_available', 'user_pending', 'merchant_pending',
            'merchant_settled', 'platform_fee', 'reserve_liability',
            'redemption_pending', 'mint_pending', 'reserve_asset'
        )),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency VARCHAR(20) NOT NULL DEFAULT 'vUSDC',
    entry_type VARCHAR(50) NOT NULL
        CHECK (entry_type IN (
            'purchase_hold', 'purchase_settle', 'purchase_refund',
            'mint_credit', 'redemption_debit', 'redemption_release', 'fee_collect',
            'deposit_confirm'
        )),
    reference_id VARCHAR(100),
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_entries_wallet ON ledger_entries(wallet);
CREATE INDEX idx_ledger_entries_tx_id ON ledger_entries(transaction_id);
CREATE INDEX idx_ledger_entries_entry_type ON ledger_entries(entry_type);
CREATE INDEX idx_ledger_entries_created ON ledger_entries(created_at);
CREATE INDEX idx_ledger_entries_debit ON ledger_entries(debit_account);
CREATE INDEX idx_ledger_entries_credit ON ledger_entries(credit_account);

-- ----------------------------------------------------------------------------
-- 9. mint_requests - Mint request lifecycle tracking
-- ----------------------------------------------------------------------------
CREATE TABLE mint_requests (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(100) NOT NULL UNIQUE,
    wallet VARCHAR(88) NOT NULL,
    asset_id BIGINT NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    reserve_deposit_tx_id VARCHAR(200),
    amount_minor BIGINT NOT NULL,
    status VARCHAR(30) NOT NULL DEFAULT 'created'
        CHECK (status IN (
            'created', 'deposit_confirmed', 'risk_checking',
            'approved', 'mint_submitted', 'minted', 'credited',
            'failed', 'cancelled'
        )),
    risk_score DECIMAL(5,2),
    approval_id VARCHAR(100),
    chain_mint_tx_id VARCHAR(200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        amount_minor > 0
        OR (amount_minor = 0 AND status IN ('created', 'failed', 'cancelled'))
    )
);

CREATE INDEX idx_mint_requests_wallet ON mint_requests(wallet);
CREATE INDEX idx_mint_requests_status ON mint_requests(status);
CREATE INDEX idx_mint_requests_asset ON mint_requests(asset_id);
CREATE UNIQUE INDEX idx_mint_requests_reserve_deposit_tx_id_unique
    ON mint_requests(reserve_deposit_tx_id)
    WHERE reserve_deposit_tx_id IS NOT NULL;

-- ----------------------------------------------------------------------------
-- 10. redemption_requests - Redemption request lifecycle tracking
-- ----------------------------------------------------------------------------
CREATE TABLE redemption_requests (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(100) NOT NULL UNIQUE,
    wallet VARCHAR(88) NOT NULL,
    asset_id BIGINT NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status VARCHAR(30) NOT NULL DEFAULT 'created'
        CHECK (status IN (
            'created', 'balance_locked', 'burn_submitted',
            'burned', 'payout_submitted', 'paid',
            'failed', 'released'
        )),
    burn_tx_id VARCHAR(200),
    payout_tx_id VARCHAR(200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_redemption_requests_wallet ON redemption_requests(wallet);
CREATE INDEX idx_redemption_requests_status ON redemption_requests(status);
CREATE INDEX idx_redemption_requests_asset ON redemption_requests(asset_id);

-- ----------------------------------------------------------------------------
-- 11. chain_txs - On-chain transaction tracking
-- ----------------------------------------------------------------------------
CREATE TABLE chain_txs (
    id BIGSERIAL PRIMARY KEY,
    network VARCHAR(50) NOT NULL,
    tx_hash VARCHAR(88) NOT NULL,
    tx_type VARCHAR(50) NOT NULL
        CHECK (tx_type IN ('deposit', 'mint', 'burn', 'payout', 'transfer')),
    status VARCHAR(30) NOT NULL DEFAULT 'submitted'
        CHECK (status IN ('submitted', 'confirmed', 'finalized', 'failed')),
    confirmations INTEGER NOT NULL DEFAULT 0 CHECK (confirmations >= 0),
    raw_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ
);

CREATE INDEX idx_chain_txs_tx_hash ON chain_txs(tx_hash);
CREATE UNIQUE INDEX idx_chain_txs_network_tx_hash_unique ON chain_txs(network, tx_hash);
CREATE INDEX idx_chain_txs_network_status ON chain_txs(network, status);
CREATE INDEX idx_chain_txs_type ON chain_txs(tx_type);

-- ----------------------------------------------------------------------------
-- 12. audit_log - Immutable audit event log
-- ----------------------------------------------------------------------------
CREATE TABLE audit_log (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(100) NOT NULL UNIQUE,
    event_type VARCHAR(100) NOT NULL,
    actor_type VARCHAR(50) NOT NULL CHECK (actor_type IN ('user', 'agent', 'system', 'admin')),
    actor_id VARCHAR(200),
    resource_type VARCHAR(100) NOT NULL,
    resource_id VARCHAR(200),
    action VARCHAR(100) NOT NULL,
    details JSONB DEFAULT '{}',
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_log_event_type ON audit_log(event_type);
CREATE INDEX idx_audit_log_resource ON audit_log(resource_type, resource_id);
CREATE INDEX idx_audit_log_created ON audit_log(created_at);
CREATE INDEX idx_audit_log_actor ON audit_log(actor_type, actor_id);

-- ----------------------------------------------------------------------------
-- 13. outbox - Outbox pattern for reliable event publishing
-- ----------------------------------------------------------------------------
CREATE TABLE outbox (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(100) NOT NULL UNIQUE,
    event_type VARCHAR(100) NOT NULL,
    aggregate_type VARCHAR(100) NOT NULL,
    aggregate_id VARCHAR(200) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'published', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX idx_outbox_status_created ON outbox(status, created_at);
CREATE INDEX idx_outbox_event_type ON outbox(event_type);

-- ----------------------------------------------------------------------------
-- Insert default assets
-- ----------------------------------------------------------------------------
INSERT INTO assets (symbol, decimals, asset_type, network, mint_address, status) VALUES
    ('vUSDC', 6, 'shadow-ledger', 'solana-mainnet', 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v', 'active'),
    ('vUSDC', 6, 'shadow-ledger', 'sonic-l2', NULL, 'active');

-- Insert default vUSDC reserve accounts (placeholder addresses)
INSERT INTO reserve_accounts (network, asset_symbol, address, confirmed_balance_minor, pending_balance_minor) VALUES
    ('solana-mainnet', 'vUSDC', 'RESERVE_WALLET_SOL_PLACEHOLDER', 0, 0),
    ('sonic-l2', 'vUSDC', 'RESERVE_WALLET_SONIC_PLACEHOLDER', 0, 0);

-- Insert default mint policies for vUSDC
INSERT INTO mint_policies (asset_id, daily_mint_limit_minor, per_wallet_limit_minor, require_manual_approval_above_minor, status)
SELECT id, 100000000000, 10000000000, 10000000000, 'active'
FROM assets WHERE symbol = 'vUSDC' AND network = 'solana-mainnet';

INSERT INTO mint_policies (asset_id, daily_mint_limit_minor, per_wallet_limit_minor, require_manual_approval_above_minor, status)
SELECT id, 100000000000, 10000000000, 10000000000, 'active'
FROM assets WHERE symbol = 'vUSDC' AND network = 'sonic-l2';

COMMIT;
