package app

import (
	"context"
	"errors"
	"reflect"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

// CorrectionBudgetBinding identifies the inspected reservation for administrator review.
func (o *budgetOwner) CorrectionBudgetBinding(ctx context.Context, job jobs.Job) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, budgetSettlementTimeout)
	defer cancel()
	record, err := o.jobReservation(ctx, job)
	if err != nil {
		return "", err
	}
	if record.JobID != job.ID || job.Valuation == nil || !reflect.DeepEqual(record.Attempt.Valuation, *job.Valuation) {
		return "", reservation.ErrIdentityConflict
	}
	return reservation.CorrectionBinding(*record), nil
}

// CorrectJob commits required settlement with the prepared job and audit records.
func (o *budgetOwner) CorrectJob(ctx context.Context, job jobs.Job, intent jobs.CorrectionIntent, publication []storage.CompareAndSwapMutation) error {
	ctx, cancel := context.WithTimeout(ctx, budgetSettlementTimeout)
	defer cancel()
	pending := job.PendingCorrection()
	if pending == nil || !reflect.DeepEqual(*pending, intent) {
		return jobs.ErrReconciliationConflict
	}
	if _, err := o.CorrectionBudgetBinding(ctx, job); err != nil {
		return err
	}
	_, err := o.ledger.CorrectWith(ctx, job.ReservationID, reservation.Correction{ID: intent.Decision.DecisionID, ExpectedBinding: intent.BudgetBinding, Actor: intent.Decision.Actor, EvidenceReference: intent.Decision.EvidenceReference, Reason: intent.Decision.Reason, Evidence: intent.Evidence()}, publication)
	if errors.Is(err, reservation.ErrIdentityConflict) {
		return errors.Join(jobs.ErrReconciliationConflict, err)
	}
	if err != nil {
		return err
	}
	return o.checkJobAuthority(ctx)
}
