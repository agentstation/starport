package jobs

import (
	"context"
	"errors"
	"maps"

	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrAccountingExpired reports optional history beyond its original retention deadline.
	ErrAccountingExpired = errors.New("jobs: optional accounting history expired")
	// ErrCorrectionReportingPending retains a correction whose accountant cannot report it.
	ErrCorrectionReportingPending = errors.New("jobs: correction reporting is pending")
)

// RequiredCorrection binds an operator decision to required settlement and job publication.
type RequiredCorrection interface {
	CorrectionBudgetBinding(context.Context, Job) (string, error)
	CorrectJob(context.Context, Job, CorrectionIntent, []storage.CompareAndSwapMutation) error
}

func (s *Service) correctionBudgetBinding(ctx context.Context, job Job) (string, error) {
	if job.ReservationID == "" {
		return "", nil
	}
	owner, ok := s.requiredSettlement.(RequiredCorrection)
	if !ok {
		return "", ErrSettlementPending
	}
	return owner.CorrectionBudgetBinding(ctx, job)
}

// PendingCorrection returns copied intent without granting permission to apply it.
func (j Job) PendingCorrection() *CorrectionIntent {
	if correctionID(j.correctionHead) == correctionID(j.correctionApplied) {
		return nil
	}
	return copyCorrection(j.correctionHead)
}

// AppliedCorrection returns the effective audited decision, if any.
func (j Job) AppliedCorrection() *CorrectionIntent { return copyCorrection(j.correctionApplied) }

func sameCorrectionRequest(intent CorrectionIntent, actor string, request ReconciliationRequest) bool {
	d := intent.Decision
	return d.Actor == actor && d.DecisionID == request.DecisionID && d.Binding == request.Binding && d.EvidenceReference == request.EvidenceReference && d.Reason == request.Reason && d.Disposition == request.Disposition && d.Tokens == request.Tokens && maps.Equal(d.Quantities, request.Quantities)
}

// InspectCorrection reads private audit history without provider traffic.
// The caller must enforce administrator authorization.
func (s *Service) InspectCorrection(ctx context.Context, account, id, decisionID string) (CorrectionAudit, error) {
	return s.records.InspectCorrection(ctx, account, id, decisionID)
}

// CorrectAdministrator retains intent before applying its required budget decision.
// Exact retries read that intent before comparing the current inspection binding.
func (s *Service) CorrectAdministrator(ctx context.Context, account, id, actor string, request ReconciliationRequest) (ReconciliationView, error) {
	if !reconciliationText(actor, 256) || actor == "anonymous" {
		return ReconciliationView{}, ErrReconciliationInvalid
	}
	if s.assets == nil {
		return ReconciliationView{}, ErrAssetNotFound
	}
	request.Quantities = maps.Clone(request.Quantities)
	for range 8 {
		job, err := s.records.Get(ctx, account, id)
		if err != nil {
			return ReconciliationView{}, err
		}
		if job.adminDecision == nil {
			return job.reconciliationView(), ErrReconciliationConflict
		}
		prior, err := s.records.InspectCorrection(ctx, account, id, request.DecisionID)
		if err == nil {
			if !sameCorrectionRequest(prior.Intent, actor, request) || prior.SupersededBy != "" {
				return job.reconciliationView(), ErrReconciliationConflict
			}
			if prior.Applied {
				return s.InspectReconciliation(ctx, account, id)
			}
		} else if !errors.Is(err, ErrCorrectionNotFound) {
			return job.reconciliationView(), err
		} else {
			job, err = s.recoverNative(ctx, job)
			if err != nil {
				return job.reconciliationView(), err
			}
			binding, err := s.correctionBudgetBinding(ctx, job)
			if err != nil {
				return job.reconciliationView(), err
			}
			if request.Binding != job.CorrectionBinding(binding) {
				return job.reconciliationView(), ErrReconciliationConflict
			}
			intent, err := job.NewCorrection(request, actor, binding, s.now())
			if err != nil {
				return job.reconciliationView(), err
			}
			job, err = s.records.CreateCorrection(ctx, job, intent)
			if errors.Is(err, storage.ErrConflict) {
				continue
			}
			if err != nil {
				return job.reconciliationView(), err
			}
		}
		settled, err := s.settleAccounting(ctx, job)
		if errors.Is(err, storage.ErrConflict) {
			continue
		}
		view, inspectErr := s.InspectReconciliation(ctx, account, id)
		if inspectErr != nil {
			return settled.reconciliationView(), errors.Join(err, inspectErr)
		}
		return view, err
	}
	return ReconciliationView{}, storage.ErrConflict
}

func (s *Service) recoverCorrection(ctx context.Context, job Job) (Job, error) {
	if job.correctionHead == nil {
		return job, nil
	}
	current, err := s.records.Get(ctx, job.Account, job.ID)
	if err != nil {
		return job, err
	}
	if current.PendingCorrection() == nil {
		if current.correctionApplied != nil {
			audit, err := s.records.InspectCorrection(ctx, current.Account, current.ID, correctionID(current.correctionApplied))
			if err != nil {
				return current, err
			}
			if !audit.Applied {
				return current, ErrCorruptRecord
			}
		}
		return current, nil
	}
	// Native receipt recovery reads retained storage only. It never submits work.
	current, err = s.recoverNative(ctx, current)
	if err != nil {
		return current, err
	}
	var commit CorrectionCommit
	if current.ReservationID != "" {
		owner, ok := s.requiredSettlement.(RequiredCorrection)
		if !ok {
			return current, ErrSettlementPending
		}
		commit = func(ctx context.Context, intent CorrectionIntent, mutations []storage.CompareAndSwapMutation) error {
			return owner.CorrectJob(ctx, current, intent, mutations)
		}
	}
	return s.records.ApplyCorrection(ctx, current, commit)
}

func (s *Service) reportCorrections(ctx context.Context, job Job) (Job, error) {
	for range 16 {
		next, err := s.records.NextCorrectionReport(ctx, job)
		if err != nil {
			return job, err
		}
		if next == nil {
			return job, nil
		}
		status := "disabled"
		if s.accountant != nil {
			reporter, ok := s.accountant.(CorrectionAccountant)
			if !ok {
				return job, ErrCorrectionReportingPending
			}
			err := reporter.RecordJobCorrection(ctx, AccountingCorrection{Original: entryFor(job), ID: next.Decision.DecisionID, PreviousID: next.PreviousAppliedID, RecordedAt: next.Decision.DecidedAt, Evidence: next.Evidence()})
			status = "delivered"
			if errors.Is(err, ErrAccountingExpired) {
				status = "expired"
			} else if err != nil {
				return job, err
			}
		}
		job, err = s.records.MarkCorrectionReported(ctx, job, *next, status)
		if err != nil {
			return job, err
		}
	}
	return job, nil
}
