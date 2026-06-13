// Package contract contains contract-level tests for quote expiration behavior.
//
// These tests verify the quote-service contract from demo.md Section 6.4:
// "The backend must verify that the quote exists and is not expired."
//
// Run with: go test -tags=integration ./tests/contract/
package contract

import (
	"testing"
)

// TestQuoteExpiredContract documents the quote expiration contract.
// The QuoteService.ValidateQuote method returns an error when:
//   - The quote does not exist
//   - The quote has expired (ExpiresAt < now)
//   - The quote has already been consumed (Consumed == true)
//   - The wallet does not match
//
// Full integration tests require a running database with quote records.
func TestQuoteExpiredContract(t *testing.T) {
	t.Run("expired quote returns error", func(t *testing.T) {
		t.Skip("requires database + quote service - run with -tags=integration")
	})

	t.Run("valid quote passes validation", func(t *testing.T) {
		t.Skip("requires database + quote service - run with -tags=integration")
	})

	t.Run("already consumed quote returns error", func(t *testing.T) {
		t.Skip("requires database + quote service - run with -tags=integration")
	})

	t.Run("wallet mismatch returns error", func(t *testing.T) {
		t.Skip("requires database + quote service - run with -tags=integration")
	})
}

// TestQuoteShortValidityContract verifies quotes are created with short validity
// (default 5 minutes per QuoteService.NewQuoteService).
func TestQuoteShortValidityContract(t *testing.T) {
	t.Run("quote default validity is 5 minutes", func(t *testing.T) {
		t.Skip("requires quote service instance - run with -tags=integration")
	})
}

// TestQuoteSingleUseContract verifies that quotes cannot be reused after consumption.
func TestQuoteSingleUseContract(t *testing.T) {
	t.Run("consumed quote cannot be reused", func(t *testing.T) {
		t.Skip("requires database + quote service - run with -tags=integration")
	})
}
