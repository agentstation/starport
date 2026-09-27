package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
)

// AccountingEntry reports a terminal job with its pinned rates and measured usage.
// Provider request identifiers remain private to the job record.
type AccountingEntry struct {
	// BillingDisposition identifies administrator-supplied billing evidence.
	BillingDisposition string
	// BillingEvidence can contain administrator evidence without a provider measurement.
	BillingEvidence *reservation.Evidence
	Valuation       *reservation.Valuation
	Measurement     *reservation.Evidence
	JobID           string
	Account         string
	KeyID           string
	Provider        string
	Model           string
	Operation       routing.Operation
	State           JobState
	// Chargeable reports completed output for display. It does not prove cost.
	Chargeable bool
	// SubmittedAt and TerminalAt bound the work. A record of the two is what
	// lets an operator tell a job that took two minutes from one that took two
	// hours, which no single stamp answers.
	SubmittedAt time.Time
	TerminalAt  time.Time
}

// Accountant records optional usage for a terminal job.
// RecordJob must accept exact retries without duplicate charges or counters.
// A delivery can succeed before the service stores its acknowledgement.
// Errors retain pending reporting and do not change the provider result.
type Accountant interface {
	RecordJob(ctx context.Context, entry AccountingEntry) error
}

// Notifier receives a best-effort terminal notification independently of billing.
// The service claims one attempt before calling JobEnded. A crash between the
// claim and the call can lose the event. This is not a durable delivery contract.
type Notifier interface {
	JobEnded(ctx context.Context, entry AccountingEntry)
}

// Meter bounds how many jobs one holder may hold open at a time.
//
// The interface is declared here for the same reason Accountant is: the limit
// vocabulary lives in another package, and a leaf that owns job state may not
// reach across for it. jobslots.Store satisfies this shape.
type Meter interface {
	Reserve(ctx context.Context, holder, claimID, jobID, kind string, bound int64) error
	Release(ctx context.Context, holder, claimID string) error
	Attachment(ctx context.Context, holder, claimID, jobID, kind string) (storage.CompareAndSwapMutation, error)
}

// settle preserves the provider result while required accounting can retry.
func (s *Service) settle(ctx context.Context, job Job) Job {
	settled, _ := s.settleAccounting(ctx, job)
	return settled
}

// settleAccounting confirms required settlement before it retries reporting.
// Notifications and slot release do not depend on settlement or reporting.
func (s *Service) settleAccounting(ctx context.Context, job Job) (Job, error) {
	if !job.State.Terminal() {
		return job, nil
	}
	job = s.settleSlot(ctx, job)
	job, notificationErr := s.notifyTerminal(ctx, job)
	if err := s.confirmSettlement(ctx, job); err != nil {
		return job, errors.Join(notificationErr, err)
	}
	if job.Accounted() {
		return job, notificationErr
	}
	if s.accountant != nil {
		if err := s.accountant.RecordJob(ctx, entryFor(job)); err != nil {
			return job, errors.Join(notificationErr, err)
		}
	}
	settled := job
	if err := settled.MarkAccounted(s.now()); err != nil {
		return job, errors.Join(notificationErr, err)
	}
	if err := s.records.Replace(ctx, job, settled); err != nil {
		return job, errors.Join(notificationErr, err)
	}
	return settled, notificationErr
}

// notifyTerminal claims one optional notification across concurrent replicas.
func (s *Service) notifyTerminal(ctx context.Context, job Job) (Job, error) {
	if s.notifier == nil || !job.NotificationAttemptedAt.IsZero() {
		return job, nil
	}
	next := job
	next.NotificationAttemptedAt = s.now()
	if err := s.records.Replace(ctx, job, next); err != nil {
		return job, err
	}
	s.notifier.JobEnded(ctx, entryFor(next))
	return next, nil
}

// entryFor projects a settled record into what the accounting seam reads.
func entryFor(job Job) AccountingEntry {
	disposition := ""
	if job.adminDecision != nil {
		disposition = "administrator_" + job.adminDecision.Disposition
	}
	return AccountingEntry{
		BillingDisposition: disposition,
		BillingEvidence:    job.BillingEvidence(), Valuation: copyValuation(job.Valuation), Measurement: copyMeasurement(job.Measurement),
		JobID:       job.ID,
		Account:     job.Account,
		KeyID:       job.KeyID,
		Provider:    job.Provider,
		Model:       job.Model,
		Operation:   job.Operation,
		State:       job.State,
		Chargeable:  job.State.Chargeable(),
		SubmittedAt: job.CreatedAt,
		TerminalAt:  job.TerminalAt,
	}
}

// reserveSlot records one claim before routing or provider work.
func (s *Service) reserveSlot(ctx context.Context, submission Submission) error {
	if s.meter == nil {
		return nil
	}
	return s.meter.Reserve(ctx, submission.Account, submission.slotID, submission.jobID, "video", submission.OutstandingBound)
}

// releaseSlot retains the same identity through a bounded cleanup attempt.
func (s *Service) releaseSlot(ctx context.Context, job Job) error {
	if s.meter == nil || job.SlotID == "" {
		return nil
	}
	commit, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.meter.Release(commit, job.Account, job.SlotID)
}

// settleSlot separates retryable release from optional usage reporting.
func (s *Service) settleSlot(ctx context.Context, job Job) Job {
	if job.SlotID == "" || job.SlotReleased || s.meter == nil {
		return job
	}
	if err := s.releaseSlot(ctx, job); err != nil {
		return job
	}
	next := job
	next.SlotReleased = true
	if err := s.records.Replace(ctx, job, next); err != nil {
		return job
	}
	return next
}
