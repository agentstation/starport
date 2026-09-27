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
	Submit(ctx context.Context, recorder SubmissionRecorder) (Acceptance, error)
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
	recovery recoveryState[Job]
	records  Repository
	assets   blob.Store
	// accountant prices a job once, at its terminal state. A service without
	// one still runs: it keeps the same stamp on the record, so a deployment
	// that later gains an accountant does not re-price the jobs it already
	// finished.
	accountant Accountant
	// notifier hears each terminal state once. A service without one tells
	// nobody, which is what a deployment with no webhook endpoint gets.
	notifier Notifier
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

// WithNotifier gives the service who hears each job's terminal state.
func WithNotifier(notifier Notifier) ServiceOption {
	return func(s *Service) { s.notifier = notifier }
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
	jobID   string
	slotID  string
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

// Submit records one selected dispatch before the provider can accept work.
// An uncertain submission retains its record and outstanding slot for recovery.
func (s *Service) Submit(ctx context.Context, open OpenRunner, submission Submission) (Job, error) {
	if open == nil {
		return Job{}, ErrRunnerRequired
	}
	submission.Account = strings.TrimSpace(submission.Account)
	if submission.Account == "" {
		return Job{}, fmt.Errorf("%w: it names no account", ErrInvalidJob)
	}
	submission.jobID = s.mint()
	if s.meter != nil {
		submission.slotID = newJobID()
	}
	if err := s.reserveSlot(ctx, submission); err != nil {
		return Job{}, err
	}
	recorder := &submissionRecorder{service: s, submission: submission}
	defer func() {
		if !recorder.attempted {
			_ = s.releaseSlot(ctx, Job{Account: submission.Account, SlotID: submission.slotID})
		}
	}()
	runner, err := open(ctx)
	if err != nil {
		return Job{}, err
	}
	if runner == nil {
		return Job{}, ErrRunnerRequired
	}
	_, err = runner.Submit(ctx, recorder)
	if recorder.attempted && !recorder.accepted {
		return recorder.job, &SubmissionError{JobID: recorder.job.ID, Cause: err}
	}
	if err != nil {
		return recorder.job, err
	}
	if !recorder.accepted {
		return Job{}, ErrSubmissionRecorderRequired
	}
	return s.settle(ctx, s.collect(ctx, runner, recorder.job)), nil
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
	if job.SubmissionPending {
		return job, &SubmissionError{JobID: job.ID}
	}
	if job.State.Terminal() {
		return s.settle(ctx, s.collect(ctx, runner, job)), nil
	}
	if s.NeedsReconciliation(job) {
		return job, nil
	}
	return s.poll(ctx, runner, job)
}

// Cancel requests cancellation and retains the provider's reported state.
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
	if job.SubmissionPending {
		return job, &SubmissionError{JobID: job.ID}
	}
	if job.State.Terminal() {
		return job, fmt.Errorf("%w: it is %s", ErrJobAlreadyEnded, job.State)
	}
	previous := job
	report, err := runner.Cancel(ctx, s.handle(job))
	if err != nil {
		return Job{}, err
	}
	if !report.State.Valid() {
		return Job{}, fmt.Errorf("%w: cancellation has no confirmed provider state", ErrInvalidJob)
	}
	if report.State == job.State {
		return job, nil
	}
	// Accepting a cancellation request does not prove that provider work ended.
	// A completion that races cancellation must retain its actual outcome.
	now := s.now()
	if err := applyReport(&job, report, now); err != nil {
		return Job{}, err
	}
	stopped, err := s.commit(ctx, previous, job)
	if err != nil {
		return Job{}, err
	}
	return s.settle(ctx, s.collect(ctx, runner, stopped)), nil
}

// handle reads the provider identifier out of the record for one call. The
// value is unexported, so nothing outside this package can build a handle, and
// the runner receives it already scoped to the one request it serves.
func (s *Service) handle(job Job) Handle {
	return Handle{Provider: job.Provider, Model: job.Model, ProviderJobID: job.providerJobID}
}

func (s *Service) commit(ctx context.Context, previous, job Job) (Job, error) {
	if err := s.records.Replace(ctx, previous, job); err != nil {
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
