package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/agentstation/starport/internal/routing"
)

var (
	// ErrRunnerRequired reports a call that named no provider side.
	ErrRunnerRequired = errors.New("jobs: a provider runner is required")
	// ErrJobAlreadyEnded reports a stop asked of a job that already ended. A
	// caller reads it as a conflict rather than as a failure of the job.
	ErrJobAlreadyEnded = errors.New("jobs: the job already ended")
)

// Handle names one accepted job at the provider that holds it.
//
// The provider job identifier travels inside this value and nowhere else. The
// service reads it off the record, which is the only thing that holds it, and
// hands it to a runner that is about to spend it on one request.
type Handle struct {
	Provider      string
	Model         string
	ProviderJobID string
}

// Acceptance is what a provider answered when it took the work.
type Acceptance struct {
	// Provider names who accepted it, and Model names what the route resolved
	// to. Both come from the route rather than from the request, because a
	// request names a catalog model and a route names a provider's own.
	Provider      string
	Model         string
	ProviderJobID string
	// State is what the provider reported at acceptance. A provider that
	// answers a finished job on the first response is not an error.
	State  JobState
	Reason string
}

// Report is what a provider answered about a job it already holds.
type Report struct {
	State  JobState
	Reason string
}

// Runner is the provider side of one job.
//
// Submit takes no request. The runner is built per request by the layer that
// holds the caller's credential policy and the request itself, so this package
// starts work without naming a single field of what the work is. That keeps
// the video shape, and every later media shape, out of the record.
type Runner interface {
	Submit(ctx context.Context) (Acceptance, error)
	Poll(ctx context.Context, handle Handle) (Report, error)
	Cancel(ctx context.Context, handle Handle) (Report, error)
}

// Service turns provider answers into records, and it is the only thing that
// writes a state.
//
// Every state a caller reads passes through the transition table here, so the
// accounting rule and the retention rule that later read the state see one
// history rather than several writers' versions of one.
type Service struct {
	records Repository
	policy  PollPolicy
	now     func() time.Time
	mint    func() string
}

// ServiceOption changes one service setting.
type ServiceOption func(*Service)

// WithPollPolicy replaces the default polling bounds.
func WithPollPolicy(policy PollPolicy) ServiceOption {
	return func(s *Service) { s.policy = policy }
}

// WithClock replaces the clock. A test states its own times rather than
// sleeping through a real window.
func WithClock(now func() time.Time) ServiceOption {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithIdentifiers replaces how a job identifier is minted.
func WithIdentifiers(mint func() string) ServiceOption {
	return func(s *Service) {
		if mint != nil {
			s.mint = mint
		}
	}
}

// NewService returns a service over one record store.
func NewService(records Repository, options ...ServiceOption) (*Service, error) {
	if records == nil {
		return nil, ErrRepositoryRequired
	}
	service := &Service{
		records: records,
		policy:  DefaultPollPolicy(),
		now:     time.Now,
		mint:    newJobID,
	}
	for _, option := range options {
		option(service)
	}
	if err := service.policy.Validate(); err != nil {
		return nil, err
	}
	return service, nil
}

// PollPolicy reports the bounds this service applies. A caller that waits
// between polls reads the interval from here rather than choosing its own.
func (s *Service) PollPolicy() PollPolicy { return s.policy }

// Submit starts one job and records it.
//
// The provider call comes before the record on purpose. A record written first
// would name a job no provider ever accepted, and a caller polling it would
// wait out the whole lifetime for an answer that was never coming.
func (s *Service) Submit(
	ctx context.Context,
	runner Runner,
	tenant string,
	operation routing.Operation,
) (Job, error) {
	if runner == nil {
		return Job{}, ErrRunnerRequired
	}
	if strings.TrimSpace(tenant) == "" {
		return Job{}, fmt.Errorf("%w: it names no tenant", ErrInvalidJob)
	}
	accepted, err := runner.Submit(ctx)
	if err != nil {
		return Job{}, err
	}
	now := s.now()
	job, err := New(s.mint(), tenant, accepted.Provider, accepted.Model, operation, now)
	if err != nil {
		return Job{}, err
	}
	if err := job.AdoptProviderJob(accepted.ProviderJobID); err != nil {
		return Job{}, err
	}
	if err := applyReport(&job, Report{State: accepted.State, Reason: accepted.Reason}, now); err != nil {
		return Job{}, err
	}
	if err := s.records.Create(ctx, job); err != nil {
		return Job{}, err
	}
	return job, nil
}

// Get reads one job without asking its provider anything. It is what a listing
// and an ownership check read.
func (s *Service) Get(ctx context.Context, tenant, id string) (Job, error) {
	return s.records.Get(ctx, tenant, id)
}

// List answers one tenant's jobs, newest first.
func (s *Service) List(ctx context.Context, tenant string, limit int) ([]Job, error) {
	return s.records.List(ctx, tenant, limit)
}

// Refresh answers the current state, asking the provider only when the answer
// could still change.
//
// A terminal job answers from the record. That is what makes a repeated poll
// free: the caller may ask as often as it likes, and neither the provider nor
// the tenant's credential is spent again, and the single usage record a
// terminal job draws is not drawn twice.
func (s *Service) Refresh(ctx context.Context, runner Runner, tenant, id string) (Job, error) {
	job, err := s.records.Get(ctx, tenant, id)
	if err != nil {
		return Job{}, err
	}
	if job.State.Terminal() {
		return job, nil
	}
	now := s.now()
	if s.policy.Spent(job, now) {
		if err := s.policy.FailSpent(&job, now); err != nil {
			return Job{}, err
		}
		return s.commit(ctx, job)
	}
	if runner == nil {
		return Job{}, ErrRunnerRequired
	}
	report, err := runner.Poll(ctx, s.handle(job))
	if err != nil {
		return Job{}, err
	}
	if report.State == job.State {
		return job, nil
	}
	if err := applyReport(&job, report, now); err != nil {
		return Job{}, err
	}
	return s.commit(ctx, job)
}

// Cancel stops one running job.
//
// A job that already ended answers ErrJobAlreadyEnded rather than moving
// again. A completed job holds an asset the tenant paid for, and a cancellation
// that rewrote it to cancelled would discard the answer and the cost record
// that goes with it.
func (s *Service) Cancel(ctx context.Context, runner Runner, tenant, id string) (Job, error) {
	if runner == nil {
		return Job{}, ErrRunnerRequired
	}
	job, err := s.records.Get(ctx, tenant, id)
	if err != nil {
		return Job{}, err
	}
	if job.State.Terminal() {
		return job, fmt.Errorf("%w: it is %s", ErrJobAlreadyEnded, job.State)
	}
	if _, err := runner.Cancel(ctx, s.handle(job)); err != nil {
		return Job{}, err
	}
	// The provider's own answer is not read back into the state. A provider
	// that reports the job as still running one moment after it accepted the
	// stop would otherwise leave a job this gateway has stopped billing for
	// still polling.
	now := s.now()
	if err := job.Transition(JobStateCancelled, now); err != nil {
		return Job{}, err
	}
	return s.commit(ctx, job)
}

// handle reads the provider identifier out of the record for one call. The
// value is unexported, so nothing outside this package can build a handle, and
// the runner receives it already scoped to the one request it serves.
func (s *Service) handle(job Job) Handle {
	return Handle{Provider: job.Provider, Model: job.Model, ProviderJobID: job.providerJobID}
}

func (s *Service) commit(ctx context.Context, job Job) (Job, error) {
	if err := s.records.Replace(ctx, job); err != nil {
		return Job{}, err
	}
	return job, nil
}

// applyReport moves a record to the state a provider reported. It exists so
// that the submit path and the poll path reach a failed state through the same
// door, and so neither can reach one without the reason a caller reads.
func applyReport(job *Job, report Report, now time.Time) error {
	if report.State == "" || report.State == job.State {
		return nil
	}
	if report.State == JobStateFailed {
		reason := strings.TrimSpace(report.Reason)
		if reason == "" {
			reason = "the provider reported a failure and stated no reason"
		}
		return job.Fail(reason, now)
	}
	return job.Transition(report.State, now)
}

// newJobID names a job the way a caller sees it. The prefix makes an
// identifier recognizable in a log line and in a request body.
func newJobID() string {
	return "job-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
