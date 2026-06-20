-- ============================================================================
-- ANCF Commerce - Solana Deposit Mint Proof Hardening
-- Version: 009
-- ============================================================================

BEGIN;

-- The Solana reserve accepts native USDC deposits for shadow-ledger vUSDC credit.
-- Store the authoritative mint so mint-service can independently reject raw
-- chain proofs that came from a spoofed SPL/Token-2022 mint.
UPDATE assets
SET mint_address = 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v'
WHERE network = 'solana-mainnet'
  AND symbol = 'vUSDC'
  AND (mint_address IS NULL OR mint_address = '');

COMMIT;
