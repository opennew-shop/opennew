-- ============================================================================
-- ANCF Commerce - Fix Ledger Entry Type Constraint
-- Version: 005
-- Description: Adds 'redemption_release' to the ledger_entries.entry_type CHECK
--              constraint. The RedemptionRelease method (used by ReleaseFunds
--              failure-recovery path) writes entries with this type, but the
--              constraint was missing it, causing failures on fund release.
--
-- SECURITY FIX: F-005-02
-- ============================================================================

BEGIN;

-- Drop and recreate the CHECK constraint to include 'redemption_release'.
-- PostgreSQL does not support ALTER TABLE ... ALTER CONSTRAINT, so we drop
-- and re-add the constraint.
ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_entry_type_check;

ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_entry_type_check
    CHECK (entry_type IN (
        'purchase_hold', 'purchase_settle', 'purchase_refund',
        'mint_credit', 'redemption_debit', 'redemption_release', 'fee_collect',
        'deposit_confirm'
    ));

COMMIT;

-- ============================================================================
-- Rollback (for reference)
-- ============================================================================
-- BEGIN;
-- ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_entry_type_check;
-- ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_entry_type_check
--     CHECK (entry_type IN (
--         'purchase_hold', 'purchase_settle', 'purchase_refund',
--         'mint_credit', 'redemption_debit', 'fee_collect',
--         'deposit_confirm'
--     ));
-- COMMIT;
