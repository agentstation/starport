package jobs

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/storage"
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

// BillingConflictRecorder commits late evidence with its required budget restriction.
// The mutation must come from the job repository on the same storage authority.
type BillingConflictRecorder interface {
	RecordJobConflict(context.Context, Job, storage.CompareAndSwapMutation) error
}

func (s *Service) publishBillingConflict(ctx context.Context, expected, next Job) error {
	if next.ReservationID == "" {
		return s.records.Replace(ctx, expected, next)
	}
	recorder, ok := s.requiredSettlement.(BillingConflictRecorder)
	if !ok {
		return ErrSettlementPending
	}
	mutation, err := s.records.Replacement(ctx, expected, next)
	if err != nil {
		return err
	}
	return recorder.RecordJobConflict(ctx, next, mutation)
}
