package app

import (
	"context"
	"strings"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// BindJob verifies the selected dispatch before it assigns the reservation.
// The caller must persist the job before it contacts the provider.
func (o *budgetOwner) BindJob(ctx context.Context, job jobs.Job) error {
	ctx, cancel := context.WithTimeout(ctx, budgetSettlementTimeout)
	defer cancel()
	record, err := o.jobReservation(ctx, job)
	if err != nil {
		return err
	}
	if record.State != reservation.Dispatched {
		return reservation.ErrTransition
	}
	if err := o.ledger.BindJob(ctx, job.ReservationID, job.ID); err != nil {
		return err
	}
	return o.checkJobAuthority(ctx)
}

// ConfirmJob recovers retained charge evidence and confirms its original job.
// A completed asset or canceled job supplies no charge evidence.
func (o *budgetOwner) ConfirmJob(ctx context.Context, job jobs.Job) error {
	ctx, cancel := context.WithTimeout(ctx, budgetSettlementTimeout)
	defer cancel()
	record, err := o.jobReservation(ctx, job)
	if err != nil {
		return err
	}
	if record.JobID != job.ID || record.JobID == "" {
		return reservation.ErrIdentityConflict
	}
	if err := o.ledger.ReconcileRetained(ctx, job.ReservationID); err != nil {
		return err
	}
	return o.checkJobAuthority(ctx)
}

func (o *budgetOwner) jobReservation(ctx context.Context, job jobs.Job) (*reservation.Record, error) {
	if o == nil || o.ledger == nil {
		return nil, reservation.ErrUnavailable
	}
	if err := o.checkJobAuthority(ctx); err != nil {
		return nil, err
	}
	record, err := o.ledger.Inspect(ctx, job.ReservationID)
	if err != nil {
		return nil, err
	}
	attempt := record.Attempt
	if job.ID == "" || job.Provider == "" || !strings.HasPrefix(job.Model, job.Provider+"/") ||
		attempt.AccountID != job.Account || attempt.KeyID != job.KeyID || attempt.OfferingID != job.Model ||
		attempt.CatalogGeneration != job.CatalogGeneration || attempt.Operation != string(job.Operation) {
		return nil, reservation.ErrIdentityConflict
	}
	return record, nil
}

func (o *budgetOwner) checkJobAuthority(ctx context.Context) error {
	if o.shared != nil {
		return o.shared.Check(ctx)
	}
	return nil
}
