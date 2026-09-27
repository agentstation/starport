package jobs

import (
	"context"
	"errors"
)

// ErrSettlementPending reports required accounting that remains unresolved.
var ErrSettlementPending = errors.New("jobs: required budget settlement is pending")

// RequiredSettlement owns a job's durable budget association and settlement.
// BindJob must verify identity and persist the association before dispatch.
// ConfirmJob must verify settlement from charge evidence, never job state.
type RequiredSettlement interface {
	BindJob(context.Context, Job) error
	ConfirmJob(context.Context, Job) error
}

// WithRequiredSettlement connects required budgets independently of reporting.
func WithRequiredSettlement(owner RequiredSettlement) ServiceOption {
	return func(s *Service) { s.requiredSettlement = owner }
}

func (s *Service) bindReservation(ctx context.Context, job Job) error {
	if job.ReservationID == "" {
		return nil
	}
	if s.requiredSettlement == nil {
		return ErrSettlementPending
	}
	return s.requiredSettlement.BindJob(ctx, job)
}

func (s *Service) confirmSettlement(ctx context.Context, job Job) error {
	if job.ReservationID == "" {
		return nil
	}
	if s.requiredSettlement == nil {
		return ErrSettlementPending
	}
	if err := s.requiredSettlement.ConfirmJob(ctx, job); err != nil {
		return errors.Join(ErrSettlementPending, err)
	}
	return nil
}

// BillingConflictRecorder blocks affected admission before late evidence is published.
// The original provider receipt remains durable if this operation fails.
type BillingConflictRecorder interface {
	RecordJobConflict(context.Context, Job) error
}

func (s *Service) recordBillingConflict(ctx context.Context, job Job) error {
	if job.ReservationID == "" {
		return nil
	}
	recorder, ok := s.requiredSettlement.(BillingConflictRecorder)
	if !ok {
		return ErrSettlementPending
	}
	return recorder.RecordJobConflict(ctx, job)
}
