package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/agentstation/starport/internal/blob"
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
//
// Fetch carries the byte bound as an argument rather than reading a setting of
// its own. This package decides what it is willing to store, because it is the
// half that stores it.
type Runner interface {
	Submit(ctx context.Context) (Acceptance, error)
	Poll(ctx context.Context, handle Handle) (Report, error)
	Cancel(ctx context.Context, handle Handle) (Report, error)
	Fetch(ctx context.Context, handle Handle, maxBytes int64) (Asset, error)
}

// Service turns provider answers into records, and it is the only thing that
// writes a state.
//
// Every state a caller reads passes through the transition table here, so the
// accounting rule and the retention rule that later read the state see one
// history rather than several writers' versions of one.
type Service struct {
	records Repository
	assets  blob.Store
	// accountant prices a job once, at its terminal state. A service without
	// one still runs: it keeps the same stamp on the record, so a deployment
	// that later gains an accountant does not re-price the jobs it already
	// finished.
	accountant Accountant
	// meter bounds how many jobs one account holds open. A service without one
	// bounds nothing, which is what a deployment with no counter gets.
	meter  Meter
	policy PollPolicy
	// retention is how long a stored asset stays readable, measured from the
	// moment this gateway stored it.
	retention time.Duration
	// maxAssetBytes bounds one stored asset. A gateway that fetched whatever a
	// provider served would size its own storage from a provider's decision.
	maxAssetBytes int64
	now           func() time.Time
	mint          func() string
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

// WithAssetStore gives the service somewhere to put a finished asset. A service
// with no store keeps records alone, which is what a deployment that configured
// no blob backend gets.
func WithAssetStore(store blob.Store) ServiceOption {
	return func(s *Service) { s.assets = store }
}

// WithRetention replaces how long a stored asset stays readable.
func WithRetention(window time.Duration) ServiceOption {
	return func(s *Service) {
		if window > 0 {
			s.retention = window
		}
	}
}

// WithAssetBound replaces the largest asset this gateway will store.
func WithAssetBound(bytes int64) ServiceOption {
	return func(s *Service) {
		if bytes > 0 {
			s.maxAssetBytes = bytes
		}
	}
}

// WithAccountant gives the service somewhere to report a finished job.
func WithAccountant(accountant Accountant) ServiceOption {
	return func(s *Service) { s.accountant = accountant }
}

// WithJobMeter gives the service the counter that bounds outstanding jobs.
func WithJobMeter(meter Meter) ServiceOption {
	return func(s *Service) { s.meter = meter }
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
		records:       records,
		policy:        DefaultPollPolicy(),
		retention:     DefaultAssetRetention,
		maxAssetBytes: DefaultMaxAssetBytes,
		now:           time.Now,
		mint:          newJobID,
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

// Retention reports how long this deployment keeps a finished asset. The
// content route states it to a caller whose asset already went, so the window
// a caller reads and the window the sweep applies are one value.
func (s *Service) Retention() time.Duration { return s.retention }

// Submission is who is asking for the work and what bounds them.
//
// It is a struct rather than a parameter list because the three values that
// travel with a submission come from three different places: the account and the
// key from the authenticated request, the operation from the route, and the
// bound from the tightest of the account's and the key's limits. A positional
// list of four strings and a number is the shape that quietly transposes.
type Submission struct {
	Account string
	// KeyID names the gateway API key that signed the request. It is optional:
	// a deployment with authentication off submits jobs no key signed.
	KeyID     string
	Operation routing.Operation
	// OutstandingBound is how many jobs this account may hold open. A value of
	// zero or less leaves the account unbounded.
	OutstandingBound int64
}

// OpenRunner builds the provider side of one submission.
//
// It is a function rather than a Runner because the account's outstanding job
// bound is decided before it is called. Building a runner resolves a route and
// a credential, and an account already at its limit must not spend either to be
// told it is at its limit.
type OpenRunner func(ctx context.Context) (Runner, error)

// Submit starts one job and records it.
//
// The provider call comes before the record on purpose. A record written first
// would name a job no provider ever accepted, and a caller polling it would
// wait out the whole lifetime for an answer that was never coming.
//
// The slot claim comes before everything, for the opposite reason. A submission
// this gateway is about to refuse must not spend routing, credential
// resolution, or provider work first, or the limit would bound what an account
// reads rather than what it pays for. Every path that then fails to reach a
// stored record gives the slot back.
func (s *Service) Submit(ctx context.Context, open OpenRunner, submission Submission) (Job, error) {
	if open == nil {
		return Job{}, ErrRunnerRequired
	}
	account := strings.TrimSpace(submission.Account)
	if account == "" {
		return Job{}, fmt.Errorf("%w: it names no account", ErrInvalidJob)
	}
	if err := s.reserveSlot(ctx, account, submission.OutstandingBound); err != nil {
		return Job{}, err
	}
	// kept turns true once a stored record holds the slot. Until then this
	// function owns it, and settle must not be able to release it twice.
	kept := false
	defer func() {
		if !kept {
			s.releaseSlot(ctx, Job{Account: account})
		}
	}()
	runner, err := open(ctx)
	if err != nil {
		return Job{}, err
	}
	if runner == nil {
		return Job{}, ErrRunnerRequired
	}
	accepted, err := runner.Submit(ctx)
	if err != nil {
		return Job{}, err
	}
	now := s.now()
	job, err := New(s.mint(), account, accepted.Provider, accepted.Model, submission.Operation, now)
	if err != nil {
		return Job{}, err
	}
	job.KeyID = submission.KeyID
	if err := job.AdoptProviderJob(accepted.ProviderJobID); err != nil {
		return Job{}, err
	}
	if err := applyReport(&job, Report{State: accepted.State, Reason: accepted.Reason}, now); err != nil {
		return Job{}, err
	}
	if err := s.records.Create(ctx, job); err != nil {
		return Job{}, err
	}
	kept = true
	// A provider that answered a finished job on the first response has an
	// asset waiting already. Collecting it here rather than on the first poll
	// means a caller that never polls still gets the bytes it paid for, and
	// settling it here means such a job never holds a slot it cannot use.
	return s.settle(ctx, s.collect(ctx, runner, job)), nil
}

// Get reads one job without asking its provider anything. It is what a listing
// and an ownership check read.
func (s *Service) Get(ctx context.Context, account, id string) (Job, error) {
	return s.records.Get(ctx, account, id)
}

// List answers one account's jobs, newest first.
func (s *Service) List(ctx context.Context, account string, limit int) ([]Job, error) {
	return s.records.List(ctx, account, limit)
}

// Refresh answers the current state, asking the provider only when the answer
// could still change.
//
// A terminal job answers from the record. That is what makes a repeated poll
// free: the caller may ask as often as it likes, and neither the provider nor
// the account's credential is spent again, and the single usage record a
// terminal job draws is not drawn twice.
func (s *Service) Refresh(ctx context.Context, runner Runner, account, id string) (Job, error) {
	job, err := s.records.Get(ctx, account, id)
	if err != nil {
		return Job{}, err
	}
	if job.State.Terminal() {
		return s.settle(ctx, s.collect(ctx, runner, job)), nil
	}
	now := s.now()
	if s.policy.Spent(job, now) {
		if err := s.policy.FailSpent(&job, now); err != nil {
			return Job{}, err
		}
		spent, err := s.commit(ctx, job)
		if err != nil {
			return Job{}, err
		}
		return s.settle(ctx, spent), nil
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
	moved, err := s.commit(ctx, job)
	if err != nil {
		return Job{}, err
	}
	return s.settle(ctx, s.collect(ctx, runner, moved)), nil
}

// Cancel stops one running job.
//
// A job that already ended answers ErrJobAlreadyEnded rather than moving
// again. A completed job holds an asset the account paid for, and a cancellation
// that rewrote it to cancelled would discard the answer and the cost record
// that goes with it.
func (s *Service) Cancel(ctx context.Context, runner Runner, account, id string) (Job, error) {
	if runner == nil {
		return Job{}, ErrRunnerRequired
	}
	job, err := s.records.Get(ctx, account, id)
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
	stopped, err := s.commit(ctx, job)
	if err != nil {
		return Job{}, err
	}
	return s.settle(ctx, stopped), nil
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
