package jobs

import (
	"context"
	"time"
)

// NeedsReconciliation reports accepted work beyond its automatic polling window.
// It does not change the provider state, release capacity, or establish a charge.
func (s *Service) NeedsReconciliation(job Job) bool {
	return !job.SubmissionPending && s.policy.Spent(job, s.now())
}

// Reconcile explicitly checks one accepted provider job under the caller's policy.
// It never submits generation or restarts automatic polling. Unconfirmed
// submissions need separate provider evidence before a caller can use their handles.
func (s *Service) Reconcile(ctx context.Context, runner Runner, account, id string) (Job, error) {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	job, err := s.records.Get(bounded, account, id)
	if err != nil {
		return Job{}, err
	}
	if job.SubmissionPending {
		return job, &SubmissionError{JobID: job.ID}
	}
	if job.State.Terminal() {
		return s.settle(bounded, s.collect(bounded, runner, job)), nil
	}
	return s.poll(bounded, runner, job)
}

func (s *Service) poll(ctx context.Context, runner Runner, job Job) (Job, error) {
	if runner == nil {
		return Job{}, ErrRunnerRequired
	}
	report, err := runner.Poll(ctx, s.handle(job))
	if err != nil {
		return Job{}, err
	}
	if !report.State.Valid() {
		return Job{}, ErrInvalidJob
	}
	if report.State == job.State {
		return job, nil
	}
	previous := job
	if err := applyReport(&job, report, s.now()); err != nil {
		return Job{}, err
	}
	moved, err := s.commit(ctx, previous, job)
	if err != nil {
		return Job{}, err
	}
	return s.settle(ctx, s.collect(ctx, runner, moved)), nil
}
