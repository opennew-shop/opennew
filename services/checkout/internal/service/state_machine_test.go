package service

import (
	"testing"

	"github.com/ancf-commerce/ancf/services/checkout/internal/model"
)

// TestOrderStateMachine tests all legal and illegal state transitions
// for the checkout order state machine.
//
// State flow:
//
//	created -> prepared -> committed -> paid -> provisioning -> completed
//	                       |          |           |
//	                       v          v           v
//	                     failed    failed       failed
//	                       |
//	                       v
//	                    refunded
func TestOrderStateMachine(t *testing.T) {
	tests := []struct {
		name  string
		from  string
		to    string
		valid bool
	}{
		// === Legal transitions ===
		{name: "created -> prepared", from: model.StatusCreated, to: model.StatusPrepared, valid: true},
		{name: "prepared -> committed", from: model.StatusPrepared, to: model.StatusCommitted, valid: true},
		{name: "committed -> paid", from: model.StatusCommitted, to: model.StatusPaid, valid: true},
		{name: "paid -> provisioning", from: model.StatusPaid, to: model.StatusProvisioning, valid: true},
		{name: "provisioning -> completed", from: model.StatusProvisioning, to: model.StatusCompleted, valid: true},
		{name: "prepared -> failed", from: model.StatusPrepared, to: model.StatusFailed, valid: true},
		{name: "committed -> failed", from: model.StatusCommitted, to: model.StatusFailed, valid: true},
		{name: "paid -> failed", from: model.StatusPaid, to: model.StatusFailed, valid: true},
		{name: "provisioning -> failed", from: model.StatusProvisioning, to: model.StatusFailed, valid: true},
		{name: "failed -> refunded", from: model.StatusFailed, to: model.StatusRefunded, valid: true},

		// === Illegal transitions ===
		// Skipping states
		{name: "created -> committed (skip prepared)", from: model.StatusCreated, to: model.StatusCommitted, valid: false},
		{name: "prepared -> completed (skip committed/paid)", from: model.StatusPrepared, to: model.StatusCompleted, valid: false},
		{name: "prepared -> paid (skip committed)", from: model.StatusPrepared, to: model.StatusPaid, valid: false},
		{name: "created -> paid (skip prepared/committed)", from: model.StatusCreated, to: model.StatusPaid, valid: false},

		// Backwards transitions
		{name: "committed -> prepared (backwards)", from: model.StatusCommitted, to: model.StatusPrepared, valid: false},
		{name: "paid -> prepared (backwards)", from: model.StatusPaid, to: model.StatusPrepared, valid: false},
		{name: "paid -> committed (backwards)", from: model.StatusPaid, to: model.StatusCommitted, valid: false},
		{name: "provisioning -> paid (backwards)", from: model.StatusProvisioning, to: model.StatusPaid, valid: false},

		// Terminal state transitions
		{name: "completed -> failed (terminal)", from: model.StatusCompleted, to: model.StatusFailed, valid: false},
		{name: "completed -> any (terminal)", from: model.StatusCompleted, to: model.StatusCreated, valid: false},
		{name: "refunded -> created (terminal)", from: model.StatusRefunded, to: model.StatusCreated, valid: false},
		{name: "refunded -> prepared (terminal)", from: model.StatusRefunded, to: model.StatusPrepared, valid: false},
		{name: "refunded -> failed (terminal to terminal)", from: model.StatusRefunded, to: model.StatusFailed, valid: false},
		{name: "completed -> refunded (terminal to terminal)", from: model.StatusCompleted, to: model.StatusRefunded, valid: false},

		// Self-transitions (not explicitly listed, should fail unless terminal)
		{name: "prepared -> prepared (self)", from: model.StatusPrepared, to: model.StatusPrepared, valid: false},
		{name: "committed -> committed (self)", from: model.StatusCommitted, to: model.StatusCommitted, valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTransition(tc.from, tc.to)
			if tc.valid && err != nil {
				t.Errorf("%s -> %s should be valid, got error: %v", tc.from, tc.to, err)
			}
			if !tc.valid && err == nil {
				t.Errorf("%s -> %s should be invalid, but transition was allowed", tc.from, tc.to)
			}
		})
	}
}

// TestIsTerminal verifies that completed and refunded are the only terminal states.
func TestIsTerminal(t *testing.T) {
	terminalStates := []string{model.StatusCompleted, model.StatusRefunded}
	nonTerminalStates := []string{
		model.StatusCreated,
		model.StatusPrepared,
		model.StatusCommitted,
		model.StatusPaid,
		model.StatusProvisioning,
		model.StatusFailed,
	}

	for _, s := range terminalStates {
		t.Run("terminal:"+s, func(t *testing.T) {
			if !IsTerminal(s) {
				t.Errorf("%s should be terminal", s)
			}
		})
	}
	for _, s := range nonTerminalStates {
		t.Run("non-terminal:"+s, func(t *testing.T) {
			if IsTerminal(s) {
				t.Errorf("%s should NOT be terminal", s)
			}
		})
	}

	// Unknown status should not be terminal.
	t.Run("unknown status", func(t *testing.T) {
		if IsTerminal("bogus_status") {
			t.Error("unknown status should not be terminal")
		}
	})
}

// TestCanTransitionTo is a convenience test for the boolean helper.
func TestCanTransitionTo(t *testing.T) {
	if !CanTransitionTo(model.StatusCreated, model.StatusPrepared) {
		t.Error("created -> prepared should be allowed")
	}
	if CanTransitionTo(model.StatusCreated, model.StatusCommitted) {
		t.Error("created -> committed should NOT be allowed")
	}
	if !CanTransitionTo(model.StatusFailed, model.StatusRefunded) {
		t.Error("failed -> refunded should be allowed")
	}
	if CanTransitionTo(model.StatusRefunded, model.StatusFailed) {
		t.Error("refunded -> failed should NOT be allowed")
	}
}

// TestValidateTransitionUnknownStatus verifies error handling for unknown statuses.
func TestValidateTransitionUnknownStatus(t *testing.T) {
	err := ValidateTransition("bogus_status", model.StatusPrepared)
	if err == nil {
		t.Error("ValidateTransition with unknown from-status should return error")
	}

	err = ValidateTransition(model.StatusPrepared, "bogus_status")
	if err == nil {
		t.Error("ValidateTransition with unknown to-status should return error")
	}
}
