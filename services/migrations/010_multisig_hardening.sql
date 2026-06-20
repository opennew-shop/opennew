-- ============================================================================
-- ANCF Commerce - Multisig Proposal Hardening
-- Version: 010
-- ============================================================================

BEGIN;

DO $$
BEGIN
	IF to_regclass('public.multisig_proposals') IS NOT NULL THEN
		ALTER TABLE multisig_proposals
			DROP CONSTRAINT IF EXISTS multisig_proposals_status_check;

		ALTER TABLE multisig_proposals
			ADD CONSTRAINT multisig_proposals_status_check
			CHECK (status IN ('pending', 'approved', 'executing', 'executed', 'rejected', 'expired'));

		CREATE UNIQUE INDEX IF NOT EXISTS idx_multisig_proposals_nonce_unique
			ON multisig_proposals(nonce);
	END IF;
END $$;

COMMIT;
