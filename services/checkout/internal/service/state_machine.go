package service

import (
	"fmt"

	"github.com/ancf-commerce/ancf/services/checkout/internal/model"
)

// allowedTransitions defines the legal status transitions for the order state machine.
//
// State flow:
//
//	created -> prepared -> committed -> paid -> provisioning -> completed
//	                            |          |           |
//	                            v          v           v
//	                          failed    failed       failed
//	                            |
//	                            v
//	                         refunded
//
// - created:     initial state after order intent creation (reserved for future use)
// - prepared:    intent created, signable payload generated, awaiting wallet signature
// - committed:   wallet signature verified, quote consumed, intent locked
// - paid:        payment confirmed (ledger debit applied)
// - provisioning: service provisioning in progress
// - completed:   service provisioned successfully (terminal state)
// - failed:      unrecoverable error at any recoverable stage
// - refunded:    funds returned after failure (terminal state)
var allowedTransitions = map[string][]string{
	model.StatusCreated:      {model.StatusPrepared},
	model.StatusPrepared:     {model.StatusCommitted, model.StatusFailed},
	model.StatusCommitted:    {model.StatusPaid, model.StatusFailed},
	model.StatusPaid:         {model.StatusProvisioning, model.StatusFailed},
	model.StatusProvisioning: {model.StatusCompleted, model.StatusFailed},
	model.StatusFailed:       {model.StatusRefunded},
	model.StatusRefunded:     {}, // terminal state
	model.StatusCompleted:    {}, // terminal state
}

// ValidateTransition checks whether a state transition from currentStatus to newStatus is allowed.
// Returns nil if the transition is valid, or an error describing why it is not.
func ValidateTransition(currentStatus, newStatus string) error {
	allowed, ok := allowedTransitions[currentStatus]
	if !ok {
		return fmt.Errorf("unknown status: %s", currentStatus)
	}

	for _, a := range allowed {
		if a == newStatus {
			return nil
		}
	}

	return fmt.Errorf("invalid transition: %s -> %s", currentStatus, newStatus)
}

// IsTerminal returns true if the given status is a terminal state (no further transitions allowed).
func IsTerminal(status string) bool {
	allowed, ok := allowedTransitions[status]
	if !ok {
		return false
	}
	return len(allowed) == 0
}

// CanTransitionTo checks whether a transition from currentStatus to targetStatus is allowed.
// Returns true if the transition is valid.
func CanTransitionTo(currentStatus, targetStatus string) bool {
	return ValidateTransition(currentStatus, targetStatus) == nil
}
