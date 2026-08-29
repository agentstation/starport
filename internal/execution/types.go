// Package execution owns attempt state, retry and fallback budgets, and the
// response-byte commitment boundary for inference execution.
package execution

import (
	"context"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/routing"
)

var (
	// ErrPlanRequired reports a missing route plan.
	ErrPlanRequired = errors.New("route plan is required")
	// ErrAttemptRequired reports a missing provider attempt function.
	ErrAttemptRequired = errors.New("provider attempt function is required")
	// ErrAttemptBudget reports exhaustion of the total logical-attempt budget.
	ErrAttemptBudget = errors.New("logical attempt budget exhausted")
	// ErrElapsedBudget reports exhaustion of the total elapsed-time budget.
	ErrElapsedBudget = errors.New("execution elapsed-time budget exhausted")
	// ErrAllAttemptsFailed reports that no planned route completed.
	ErrAllAttemptsFailed = errors.New("all planned attempts failed")
)

// AttemptAction tells the executor how an attempt failure affects the current
// route. The executor still applies the one total attempt budget.
type AttemptAction uint8

const (
	// AttemptActionDefault applies normal retry and route-fallback policy.
	AttemptActionDefault AttemptAction = iota
	// AttemptActionContinueRoute consumes another attempt on the same route
	// without changing provider-health state or applying retry backoff.
	AttemptActionContinueRoute
	// AttemptActionFallbackRoute moves directly to the next planned route
	// without changing provider-health state or applying retry policy.
	AttemptActionFallbackRoute
	// AttemptActionStop ends execution without changing provider-health state.
	AttemptActionStop
)

// State is one state in the logical-attempt state machine.
type State string

const (
	// StateQueued identifies an attempt that has not started.
	StateQueued State = "queued"
	// StateRunning identifies an active provider attempt.
	StateRunning State = "running"
	// StateSucceeded identifies a completed provider attempt.
	StateSucceeded State = "succeeded"
	// StateFailed identifies a provider attempt that returned a failure.
	StateFailed State = "failed"
	// StateSkipped identifies a route that availability policy rejected.
	StateSkipped State = "skipped"
	// StateCanceled identifies an attempt stopped by context cancellation.
	StateCanceled State = "canceled"
)

// Transition is one timestamped attempt state transition.
type Transition struct {
	From State
	To   State
	At   time.Time
}

// AttemptEvidence records one provider invocation or availability skip.
type AttemptEvidence struct {
	Number     int
	Route      routing.Route
	Retry      int
	State      State
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
	Failure    *failure.Failure
	// Credential names which credential plane paid for this attempt. It is
	// evidence about the attempt in the same sense the route is, and a
	// fallback can move between planes, so it is recorded per attempt rather
	// than once per request. A skipped attempt carries none.
	Credential  CredentialEvidence
	Transitions []Transition
}

// CredentialOwner identifies the request credential plane used for one
// provider attempt. It contains no account identity or credential material.
type CredentialOwner string

const (
	// CredentialOwnerOperator identifies deployment-owned inference material.
	CredentialOwnerOperator CredentialOwner = "operator"
	// CredentialOwnerAccount identifies request-scoped account BYOK material.
	CredentialOwnerAccount CredentialOwner = "account"
)

// CredentialEvidence identifies one selected credential version without
// exposing its values or source reference.
type CredentialEvidence struct {
	Owner CredentialOwner
	// Source is the caller's name for the plane the credential came from.
	// Execution never interprets it: the owner is what decides whether an
	// attempt may move the shared availability state, and the source is
	// carried only so an operator can later see which of the owner's planes
	// answered. Splitting the two keeps the credential vocabulary with the
	// package that owns it instead of mirroring it here.
	Source          string
	MaterialVersion string
	Accepted        bool
}

// AttemptOutcome is the safe state-transition evidence from one provider
// invocation.
type AttemptOutcome struct {
	Route      routing.Route
	Credential CredentialEvidence
	Failure    *failure.Failure
}

// OutcomePublisher receives completed provider invocation outcomes. It must
// not block on external I/O.
type OutcomePublisher interface {
	PublishOutcome(AttemptOutcome)
}

// Config defines the one total execution budget.
type Config struct {
	MaxAttempts        int
	MaxRetriesPerRoute int
	MaxElapsed         time.Duration
	RetryBackoff       time.Duration
	BackoffMultiplier  float64
	MaxBackoff         time.Duration
}

// DefaultConfig returns bounded production defaults. A retry and a fallback
// consume the same MaxAttempts budget.
func DefaultConfig() Config {
	return Config{
		MaxAttempts:        3,
		MaxRetriesPerRoute: 0,
		MaxElapsed:         2 * time.Minute,
		RetryBackoff:       100 * time.Millisecond,
		BackoffMultiplier:  2,
		MaxBackoff:         2 * time.Second,
	}
}

// Clock supplies deterministic attempt time and waits.
type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

// Availability owns attempt admission and offering outcome transitions.
type Availability interface {
	Acquire(routing.Route) bool
	Release(routing.Route)
	RecordSuccess(routing.Route, time.Duration)
	RecordFailure(routing.Route, *failure.Failure, time.Duration)
}

// ChatAttempt makes one non-streaming provider invocation.
type ChatAttempt func(context.Context, routing.Attempt) (*inference.ChatResponse, *failure.Failure, AttemptAction)

// ChatResult is one canonical completed result with execution evidence.
type ChatResult struct {
	Response   inference.ChatResponse
	Route      routing.Route
	Attempts   []AttemptEvidence
	StartedAt  time.Time
	FinishedAt time.Time
}

// EmbeddingAttempt makes one non-streaming provider invocation.
type EmbeddingAttempt func(context.Context, routing.Attempt) (*inference.EmbeddingResponse, *failure.Failure, AttemptAction)

// EmbeddingResult is one canonical completed embedding result with execution evidence.
type EmbeddingResult struct {
	Response   inference.EmbeddingResponse
	Route      routing.Route
	Attempts   []AttemptEvidence
	StartedAt  time.Time
	FinishedAt time.Time
}

// Attempt makes one non-streaming provider invocation for any canonical
// response type. Chat and embeddings each have a named alias below, because
// they are the two the gateway served before the media operations arrived.
type Attempt[Response any] func(context.Context, routing.Attempt) (*Response, *failure.Failure, AttemptAction)

// Result is one canonical completed result with execution evidence.
type Result[Response any] struct {
	Response   Response
	Route      routing.Route
	Attempts   []AttemptEvidence
	StartedAt  time.Time
	FinishedAt time.Time
}

// Stream is a provider-neutral inference event stream.
type Stream interface {
	Read() (*inference.StreamEvent, error)
	Close() error
}

// StreamAttempt starts one provider stream. Read failures should be normalized
// as *failure.Failure values. Wrap a pre-commit read error with
// WithAttemptAction when it must continue the same route.
type StreamAttempt func(context.Context, routing.Attempt) (Stream, *failure.Failure, AttemptAction)

// WithAttemptAction annotates a stream read failure with execution policy.
// The wrapped error remains available through errors.Is and errors.As.
func WithAttemptAction(err error, action AttemptAction) error {
	if err == nil || action == AttemptActionDefault {
		return err
	}
	return &attemptActionError{error: err, action: action}
}

type attemptActionError struct {
	error
	action AttemptAction
}

// ManagedStream exposes execution evidence without changing the protocol stream contract.
type ManagedStream interface {
	Stream
	Attempts() []AttemptEvidence
	Committed() bool
	ModelUsed() string
}

// Error reports terminal execution evidence and preserves the normalized failure.
type Error struct {
	Reason   error
	Failure  *failure.Failure
	Attempts []AttemptEvidence
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Failure != nil {
		return e.Failure.Error()
	}
	if e.Reason != nil {
		return e.Reason.Error()
	}
	return ErrAllAttemptsFailed.Error()
}

// Unwrap preserves the canonical failure for errors.As.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Failure
}

// Is preserves the terminal budget reason for errors.Is.
func (e *Error) Is(target error) bool {
	return e != nil && e.Reason != nil && errors.Is(e.Reason, target)
}
