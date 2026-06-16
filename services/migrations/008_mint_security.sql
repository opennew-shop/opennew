-- ============================================================================
-- ANCF Commerce - Mint Security Hardening
-- Version: 008
-- ============================================================================

BEGIN;

-- Deposit intents are created before the user transfers funds, so amount=0 is
-- valid only while the request is still in the initial created state.
ALTER TABLE mint_requests
	DROP CONSTRAINT IF EXISTS mint_requests_amount_minor_check;

ALTER TABLE mint_requests
	ADD CONSTRAINT mint_requests_amount_minor_check
	CHECK (
		amount_minor > 0
		OR (amount_minor = 0 AND status IN ('created', 'failed', 'cancelled'))
	);

-- A finalized on-chain deposit may be consumed by exactly one mint request.
CREATE UNIQUE INDEX IF NOT EXISTS idx_mint_requests_reserve_deposit_tx_id_unique
	ON mint_requests(reserve_deposit_tx_id)
	WHERE reserve_deposit_tx_id IS NOT NULL;

-- Chain transaction hashes are unique only within a network.
CREATE UNIQUE INDEX IF NOT EXISTS idx_chain_txs_network_tx_hash_unique
	ON chain_txs(network, tx_hash);

COMMIT;
