package execution

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/routing"
)

// Executor applies one total attempt and elapsed-time budget to one route plan.
type Executor struct {
	config       Config
	clock        Clock
	availability Availability
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// New creates an executor with explicit total-budget policy.
func New(config Config, clock Clock, availability Availability) (*Executor, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Executor{config: config, clock: clock, availability: availability}, nil
}

// ExecuteChat executes one immutable plan for a non-streaming request.
func (e *Executor) ExecuteChat(
	ctx context.Context,
	plan *routing.Plan,
	attempt ChatAttempt,
) (*ChatResult, error) {
	if plan == nil {
		return nil, ErrPlanRequired
	}
	if attempt == nil {
		return nil, ErrAttemptRequired
	}
	result, err := executeInference(ctx, e, plan, func(
		attemptCtx context.Context,
		planned routing.Attempt,
	) (*inference.ChatResponse, *failure.Failure, AttemptAction) {
		return attempt(attemptCtx, planned)
	}, inference.ChatResponse.Clone)
	if err != nil {
		return nil, err
	}
	return &ChatResult{
		Response: result.response, Route: result.route, Attempts: result.attempts,
		StartedAt: result.startedAt, FinishedAt: result.finishedAt,
	}, nil
}

// ExecuteEmbedding executes one immutable plan for an embedding request.
func (e *Executor) ExecuteEmbedding(
	ctx context.Context,
	plan *routing.Plan,
	attempt EmbeddingAttempt,
) (*EmbeddingResult, error) {
	if plan == nil {
		return nil, ErrPlanRequired
	}
	if attempt == nil {
		return nil, ErrAttemptRequired
	}
	result, err := executeInference(ctx, e, plan, func(
		attemptCtx context.Context,
		planned routing.Attempt,
	) (*inference.EmbeddingResponse, *failure.Failure, AttemptAction) {
		return attempt(attemptCtx, planned)
	}, inference.EmbeddingResponse.Clone)
	if err != nil {
		return nil, err
	}
	return &EmbeddingResult{
		Response: result.response, Route: result.route, Attempts: result.attempts,
		StartedAt: result.startedAt, FinishedAt: result.finishedAt,
	}, nil
}

type inferenceResult[T any] struct {
	response   T
	route      routing.Route
	attempts   []AttemptEvidence
	startedAt  time.Time
	finishedAt time.Time
}

func executeInference[T any](
	ctx context.Context,
	e *Executor,
	plan *routing.Plan,
	attempt func(context.Context, routing.Attempt) (*T, *failure.Failure, AttemptAction),
	clone func(T) T,
) (*inferenceResult[T], error) {
	if plan == nil {
		return nil, ErrPlanRequired
	}
	if attempt == nil {
		return nil, ErrAttemptRequired
	}

	session := newSession(e, plan)
	for {
		planned, evidenceIndex, err := session.begin(ctx)
		if err != nil {
			return nil, err
		}

		attemptCtx, cancel := session.attemptContext(ctx)
		response, providerFailure, action := attempt(attemptCtx, planned)
		providerFailure = session.normalizeOutcome(attemptCtx, response != nil, providerFailure)
		cancel()
		if providerFailure == nil {
			session.succeed(evidenceIndex)
			return &inferenceResult[T]{
				response:   clone(*response),
				route:      planned.Route,
				attempts:   session.evidenceCopy(),
				startedAt:  session.startedAt,
				finishedAt: e.clock.Now(),
			}, nil
		}

		decision := session.fail(evidenceIndex, providerFailure, action)
		if decision == decisionStop {
			return nil, session.terminalError(ErrAllAttemptsFailed)
		}
		if err := session.wait(ctx, decision); err != nil {
			return nil, session.cancelError(err)
		}
	}
}

type decision uint8

const (
	decisionStop decision = iota
	decisionContinueRoute
	decisionRetry
	decisionFallback
)

type session struct {
	executor *Executor
	attempts []routing.Attempt

	startedAt        time.Time
	routeIndex       int
	routeRetry       int
	actualAttempts   int
	evidence         []AttemptEvidence
	lastFailure      *failure.Failure
	continueRoute    bool
	availabilityHeld bool
}

func newSession(executor *Executor, plan *routing.Plan) *session {
	return &session{
		executor:  executor,
		attempts:  plan.Attempts(),
		startedAt: executor.clock.Now(),
		evidence:  make([]AttemptEvidence, 0, executor.config.MaxAttempts),
	}
}

func (s *session) begin(ctx context.Context) (routing.Attempt, int, error) {
	for {
		if err := ctx.Err(); err != nil {
			s.releaseAvailability()
			return routing.Attempt{}, -1, s.cancelError(err)
		}
		if s.elapsedBudgetExhausted() {
			s.releaseAvailability()
			return routing.Attempt{}, -1, s.terminalError(ErrElapsedBudget)
		}
		if s.actualAttempts >= s.executor.config.MaxAttempts {
			s.releaseAvailability()
			return routing.Attempt{}, -1, s.terminalError(ErrAttemptBudget)
		}
		if s.routeIndex >= len(s.attempts) {
			s.releaseAvailability()
			return routing.Attempt{}, -1, s.terminalError(ErrAllAttemptsFailed)
		}

		planned := s.attempts[s.routeIndex]
		continuing := s.continueRoute
		s.continueRoute = false
		if !continuing && s.executor.availability != nil && !s.executor.availability.Acquire(planned.Route) {
			now := s.executor.clock.Now()
			s.evidence = append(s.evidence, AttemptEvidence{
				Number:     len(s.evidence) + 1,
				Route:      planned.Route,
				Retry:      s.routeRetry,
				State:      StateSkipped,
				StartedAt:  now,
				FinishedAt: now,
				Transitions: []Transition{
					{To: StateQueued, At: now},
					{From: StateQueued, To: StateSkipped, At: now},
				},
			})
			s.routeIndex++
			s.routeRetry = 0
			continue
		}
		if !continuing && s.executor.availability != nil {
			s.availabilityHeld = true
		}

		now := s.executor.clock.Now()
		s.actualAttempts++
		s.evidence = append(s.evidence, AttemptEvidence{
			Number:    len(s.evidence) + 1,
			Route:     planned.Route,
			Retry:     s.routeRetry,
			State:     StateRunning,
			StartedAt: now,
			Transitions: []Transition{
				{To: StateQueued, At: now},
				{From: StateQueued, To: StateRunning, At: now},
			},
		})
		return planned, len(s.evidence) - 1, nil
	}
}

func (s *session) succeed(evidenceIndex int) {
	now := s.executor.clock.Now()
	evidence := &s.evidence[evidenceIndex]
	evidence.State = StateSucceeded
	evidence.FinishedAt = now
	evidence.Duration = nonNegativeDuration(evidence.StartedAt, now)
	evidence.Transitions = append(evidence.Transitions, Transition{From: StateRunning, To: StateSucceeded, At: now})
	if s.executor.availability != nil {
		s.executor.availability.RecordSuccess(evidence.Route, evidence.Duration)
		s.availabilityHeld = false
	}
}

func (s *session) fail(
	evidenceIndex int,
	providerFailure *failure.Failure,
	action AttemptAction,
) decision {
	now := s.executor.clock.Now()
	evidence := &s.evidence[evidenceIndex]
	state := StateFailed
	if providerFailure.Kind() == failure.Canceled {
		state = StateCanceled
	}
	evidence.State = state
	evidence.FinishedAt = now
	evidence.Duration = nonNegativeDuration(evidence.StartedAt, now)
	evidence.Failure = providerFailure
	evidence.Transitions = append(evidence.Transitions, Transition{From: StateRunning, To: state, At: now})
	s.lastFailure = providerFailure
	if state == StateCanceled {
		if action == AttemptActionStop {
			s.releaseAvailability()
			return decisionStop
		}
		s.recordFailure(evidence, providerFailure)
		return decisionStop
	}
	if action == AttemptActionContinueRoute && s.actualAttempts < s.executor.config.MaxAttempts {
		s.continueRoute = true
		return decisionContinueRoute
	}
	if action == AttemptActionContinueRoute {
		s.releaseAvailability()
		return decisionStop
	}
	if action == AttemptActionFallbackRoute {
		s.releaseAvailability()
		if s.routeIndex+1 < len(s.attempts) && s.actualAttempts < s.executor.config.MaxAttempts {
			s.routeIndex++
			s.routeRetry = 0
			return decisionFallback
		}
		return decisionStop
	}
	if action == AttemptActionStop {
		s.releaseAvailability()
		return decisionStop
	}
	s.recordFailure(evidence, providerFailure)
	if canRetry(providerFailure) && s.routeRetry < s.executor.config.MaxRetriesPerRoute && s.actualAttempts < s.executor.config.MaxAttempts {
		s.routeRetry++
		return decisionRetry
	}
	if CanFallback(providerFailure) && s.routeIndex+1 < len(s.attempts) && s.actualAttempts < s.executor.config.MaxAttempts {
		s.routeIndex++
		s.routeRetry = 0
		return decisionFallback
	}
	return decisionStop
}

func (s *session) recordFailure(evidence *AttemptEvidence, providerFailure *failure.Failure) {
	if s.executor.availability == nil {
		return
	}
	s.executor.availability.RecordFailure(evidence.Route, providerFailure, evidence.Duration)
	s.availabilityHeld = false
}

func (s *session) releaseAvailability() {
	if !s.availabilityHeld || s.executor.availability == nil || s.routeIndex >= len(s.attempts) {
		return
	}
	s.executor.availability.Release(s.attempts[s.routeIndex].Route)
	s.availabilityHeld = false
}

func (s *session) wait(ctx context.Context, next decision) error {
	if next != decisionRetry {
		return nil
	}
	delay := s.retryDelay()
	if delay <= 0 {
		return nil
	}
	return s.executor.clock.Sleep(ctx, delay)
}

func (s *session) retryDelay() time.Duration {
	config := s.executor.config
	if config.RetryBackoff <= 0 || s.routeRetry <= 0 {
		return 0
	}
	multiplier := math.Pow(config.BackoffMultiplier, float64(s.routeRetry-1))
	delay := time.Duration(float64(config.RetryBackoff) * multiplier)
	if delay < 0 || (config.MaxBackoff > 0 && delay > config.MaxBackoff) {
		return config.MaxBackoff
	}
	return delay
}

func (s *session) attemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.executor.config.MaxElapsed <= 0 {
		return context.WithCancel(ctx)
	}
	remaining := s.executor.config.MaxElapsed - nonNegativeDuration(s.startedAt, s.executor.clock.Now())
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	return context.WithTimeout(ctx, remaining)
}

func (s *session) normalizeOutcome(ctx context.Context, hasResult bool, providerFailure *failure.Failure) *failure.Failure {
	if err := ctx.Err(); err != nil {
		return contextFailure(err)
	}
	if s.elapsedBudgetExhausted() {
		return failure.New(failure.Timeout, "The execution time budget was exhausted.", false, failure.ProviderDetails{}, ErrElapsedBudget)
	}
	if providerFailure != nil {
		return providerFailure
	}
	if !hasResult {
		return failure.New(failure.Internal, "The provider returned no result.", false, failure.ProviderDetails{}, nil)
	}
	return nil
}

func (s *session) elapsedBudgetExhausted() bool {
	return s.executor.config.MaxElapsed > 0 &&
		nonNegativeDuration(s.startedAt, s.executor.clock.Now()) >= s.executor.config.MaxElapsed
}

func (s *session) terminalError(reason error) *Error {
	providerFailure := s.lastFailure
	if providerFailure == nil {
		kind := failure.ProviderUnavailable
		message := "No provider attempt completed."
		if reason == ErrElapsedBudget {
			kind = failure.Timeout
			message = "The execution time budget was exhausted."
		}
		providerFailure = failure.New(kind, message, false, failure.ProviderDetails{}, reason)
	}
	return &Error{Reason: reason, Failure: providerFailure, Attempts: s.evidenceCopy()}
}

func (s *session) cancelError(err error) *Error {
	providerFailure := contextFailure(err)
	s.lastFailure = providerFailure
	return &Error{Reason: err, Failure: providerFailure, Attempts: s.evidenceCopy()}
}

func (s *session) evidenceCopy() []AttemptEvidence {
	result := make([]AttemptEvidence, len(s.evidence))
	copy(result, s.evidence)
	for index := range result {
		result[index].Transitions = append([]Transition(nil), s.evidence[index].Transitions...)
	}
	return result
}

// CanFallback reports whether policy can move to the next planned route.
func CanFallback(providerFailure *failure.Failure) bool {
	if providerFailure == nil {
		return false
	}
	switch providerFailure.Kind() {
	case failure.RateLimit, failure.NotFound, failure.ContextLimit, failure.ContentBlocked, failure.ProviderUnavailable, failure.Timeout:
		return true
	default:
		return false
	}
}

func canRetry(providerFailure *failure.Failure) bool {
	return providerFailure != nil && providerFailure.Retryable()
}

func contextFailure(err error) *failure.Failure {
	kind := failure.Canceled
	message := "The request was canceled."
	if errors.Is(err, context.DeadlineExceeded) {
		kind = failure.Timeout
		message = "The execution time budget was exhausted."
	}
	return failure.New(kind, message, false, failure.ProviderDetails{}, err)
}

func nonNegativeDuration(start, end time.Time) time.Duration {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

func validateConfig(config Config) error {
	if config.MaxAttempts <= 0 {
		return fmt.Errorf("MaxAttempts must be positive")
	}
	if config.MaxRetriesPerRoute < 0 {
		return fmt.Errorf("MaxRetriesPerRoute must not be negative")
	}
	if config.MaxElapsed <= 0 {
		return fmt.Errorf("MaxElapsed must be positive")
	}
	if config.RetryBackoff < 0 || config.MaxBackoff < 0 {
		return fmt.Errorf("retry backoff values must not be negative")
	}
	if config.BackoffMultiplier < 1 {
		return fmt.Errorf("BackoffMultiplier must be at least 1")
	}
	return nil
}
