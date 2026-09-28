package admission

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/limits/reservation"
)

// Finish durably reconciles authoritative usage before reporting completion.
// Nil evidence preserves the full reservation. Estimates, terminal job states,
// cancellation, and transport failures are not authoritative usage evidence.
// Required settlement uses a bounded context even when the caller disconnects.
func (t Ticket) Finish(ctx context.Context, evidence *reservation.Evidence) error {
	if t.owner == nil {
		return nil
	}
	settlement, cancel := context.WithTimeout(context.WithoutCancel(ctx), t.owner.settlementTimeout)
	defer cancel()
	if evidence == nil {
		if err := t.owner.ledger.MarkUncertain(settlement, t.id, "provider_usage_unconfirmed"); err != nil {
			return &Error{AttemptID: t.id, Cause: err}
		}
		return nil
	}
	owned := *evidence
	if t.tokenOnly {
		owned.Quantities = nil
	}
	if err := t.owner.ledger.Reconcile(settlement, t.id, owned); err != nil {
		mark := t.owner.ledger.RetainEvidence(settlement, t.id, owned)
		return &Error{AttemptID: t.id, Cause: errors.Join(err, mark)}
	}
	return nil
}
