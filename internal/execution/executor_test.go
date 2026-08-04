package execution

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/availability"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestAttemptStateAndRetryBudgetContract(t *testing.T) {
	t.Run("retry and fallback share one total budget", func(t *testing.T) {
		clock := newFakeClock()
		executor := newTestExecutor(t, clock, Config{
			MaxAttempts: 3, MaxRetriesPerRoute: 1, MaxElapsed: time.Minute,
			RetryBackoff: time.Second, BackoffMultiplier: 2, MaxBackoff: time.Minute,
		})
		plan := testPlan(t, "provider-a/model", "provider-b/model")
		calls := 0
		result, err := executor.ExecuteChat(context.Background(), plan, func(_ context.Context, attempt routing.Attempt) (*inference.ChatResponse, *failure.Failure) {
			calls++
			if attempt.Route.ProviderID == "provider-a" {
				return nil, retryableFailure(attempt.Route.ProviderID)
			}
			return &inference.ChatResponse{ID: "ok", ModelUsed: attempt.Route.ID()}, nil
		})
		require.NoError(t, err)
		require.Equal(t, 3, calls)
		require.Equal(t, "provider-b/model", result.Route.ID())
		require.Len(t, result.Attempts, 3)
		require.Equal(t, []State{StateFailed, StateFailed, StateSucceeded}, evidenceStates(result.Attempts))
		require.Equal(t, time.Second, clock.Now().Sub(time.Unix(100, 0)))
		assertValidTransitions(t, result.Attempts)
	})

	t.Run("attempt budget is a hard cap", func(t *testing.T) {
		executor := newTestExecutor(t, newFakeClock(), Config{
			MaxAttempts: 2, MaxRetriesPerRoute: 1, MaxElapsed: time.Minute,
			RetryBackoff: time.Second, BackoffMultiplier: 2, MaxBackoff: time.Minute,
		})
		calls := 0
		_, err := executor.ExecuteChat(context.Background(), testPlan(t, "provider-a/model", "provider-b/model"), func(_ context.Context, attempt routing.Attempt) (*inference.ChatResponse, *failure.Failure) {
			calls++
			return nil, retryableFailure(attempt.Route.ProviderID)
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrAllAttemptsFailed)
		require.Equal(t, 2, calls)
	})

	t.Run("cancellation is terminal", func(t *testing.T) {
		executor := newTestExecutor(t, newFakeClock(), testConfig())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := executor.ExecuteChat(ctx, testPlan(t, "provider-a/model"), func(context.Context, routing.Attempt) (*inference.ChatResponse, *failure.Failure) {
			t.Fatal("canceled execution invoked a provider")
			return nil, nil
		})
		require.Error(t, err)
		var executionError *Error
		require.ErrorAs(t, err, &executionError)
		require.Equal(t, failure.Canceled, executionError.Failure.Kind())
	})

	t.Run("fake clock enforces elapsed budget", func(t *testing.T) {
		clock := newFakeClock()
		config := testConfig()
		config.MaxElapsed = 5 * time.Second
		executor := newTestExecutor(t, clock, config)
		_, err := executor.ExecuteChat(context.Background(), testPlan(t, "provider-a/model"), func(context.Context, routing.Attempt) (*inference.ChatResponse, *failure.Failure) {
			clock.Advance(6 * time.Second)
			return &inference.ChatResponse{ID: "late"}, nil
		})
		require.Error(t, err)
		var executionError *Error
		require.ErrorAs(t, err, &executionError)
		require.Equal(t, failure.Timeout, executionError.Failure.Kind())
	})

	t.Run("stream can fall back before first event", func(t *testing.T) {
		executor := newTestExecutor(t, newFakeClock(), testConfig())
		starts := 0
		stream, err := executor.StartChatStream(context.Background(), testPlan(t, "provider-a/model", "provider-b/model"), func(_ context.Context, attempt routing.Attempt) (Stream, *failure.Failure) {
			starts++
			if attempt.Route.ProviderID == "provider-a" {
				return &scriptedStream{errors: []error{retryableFailure("provider-a")}}, nil
			}
			return &scriptedStream{events: []*inference.StreamEvent{{Kind: inference.StreamDelta, ModelUsed: attempt.Route.ID()}}}, nil
		})
		require.NoError(t, err)
		event, err := stream.Read()
		require.NoError(t, err)
		require.Equal(t, "provider-b/model", event.ModelUsed)
		require.True(t, stream.Committed())
		require.Equal(t, 2, starts)
		require.Equal(t, []State{StateFailed, StateRunning}, evidenceStates(stream.Attempts()))
		require.NoError(t, stream.Close())
	})

	t.Run("stream never falls back after first event", func(t *testing.T) {
		executor := newTestExecutor(t, newFakeClock(), testConfig())
		starts := 0
		stream, err := executor.StartChatStream(context.Background(), testPlan(t, "provider-a/model", "provider-b/model"), func(_ context.Context, attempt routing.Attempt) (Stream, *failure.Failure) {
			starts++
			return &scriptedStream{
				events: []*inference.StreamEvent{{Kind: inference.StreamDelta, ModelUsed: attempt.Route.ID()}},
				errors: []error{nil, retryableFailure(attempt.Route.ProviderID)},
			}, nil
		})
		require.NoError(t, err)
		_, err = stream.Read()
		require.NoError(t, err)
		_, err = stream.Read()
		require.Error(t, err)
		require.Equal(t, 1, starts)
		require.Equal(t, []State{StateFailed}, evidenceStates(stream.Attempts()))
	})

	t.Run("stream returns an event before its terminal error", func(t *testing.T) {
		executor := newTestExecutor(t, newFakeClock(), testConfig())
		starts := 0
		stream, err := executor.StartChatStream(context.Background(), testPlan(t, "provider-a/model", "provider-b/model"), func(_ context.Context, attempt routing.Attempt) (Stream, *failure.Failure) {
			starts++
			return &scriptedStream{
				events: []*inference.StreamEvent{{Kind: inference.StreamDelta, ModelUsed: attempt.Route.ID()}},
				errors: []error{retryableFailure(attempt.Route.ProviderID)},
			}, nil
		})
		require.NoError(t, err)

		event, err := stream.Read()
		require.NoError(t, err)
		require.Equal(t, "provider-a/model", event.ModelUsed)
		_, err = stream.Read()
		require.Error(t, err)
		require.Equal(t, 1, starts)
		require.Equal(t, []State{StateFailed}, evidenceStates(stream.Attempts()))
	})

	t.Run("close interrupts a blocked provider read", func(t *testing.T) {
		executor := newTestExecutor(t, newFakeClock(), testConfig())
		providerStream := newCloseDrivenStream()
		stream, err := executor.StartChatStream(context.Background(), testPlan(t, "provider-a/model"), func(context.Context, routing.Attempt) (Stream, *failure.Failure) {
			return providerStream, nil
		})
		require.NoError(t, err)

		readDone := make(chan error, 1)
		go func() {
			_, readErr := stream.Read()
			readDone <- readErr
		}()
		<-providerStream.started

		closeDone := make(chan error, 1)
		go func() { closeDone <- stream.Close() }()
		select {
		case closeErr := <-closeDone:
			require.NoError(t, closeErr)
		case <-time.After(time.Second):
			t.Fatal("Close waited for the blocked provider Read")
		}
		select {
		case readErr := <-readDone:
			require.ErrorIs(t, readErr, io.EOF)
		case <-time.After(time.Second):
			t.Fatal("provider Read did not stop after Close")
		}
		require.Equal(t, []State{StateCanceled}, evidenceStates(stream.Attempts()))
	})

	t.Run("stream start failures use the same budget", func(t *testing.T) {
		executor := newTestExecutor(t, newFakeClock(), testConfig())
		starts := 0
		stream, err := executor.StartChatStream(context.Background(), testPlan(t, "provider-a/model", "provider-b/model"), func(_ context.Context, attempt routing.Attempt) (Stream, *failure.Failure) {
			starts++
			if attempt.Route.ProviderID == "provider-a" {
				return nil, retryableFailure(attempt.Route.ProviderID)
			}
			return &scriptedStream{}, nil
		})
		require.NoError(t, err)
		require.Equal(t, 2, starts)
		require.Equal(t, "provider-b/model", stream.ModelUsed())
		require.NoError(t, stream.Close())
	})

	t.Run("half-open offering recovers after a successful probe", func(t *testing.T) {
		clock := newFakeClock()
		tracker, err := availability.New(
			availability.Config{FailureThreshold: 1, OpenDuration: time.Minute},
			clock,
			nil,
		)
		require.NoError(t, err)
		config := testConfig()
		config.MaxAttempts = 2
		config.MaxRetriesPerRoute = 1
		executor, err := New(config, clock, tracker)
		require.NoError(t, err)
		plan := testPlan(t, "provider-a/model")

		_, err = executor.ExecuteChat(context.Background(), plan, func(_ context.Context, attempt routing.Attempt) (*inference.ChatResponse, *failure.Failure) {
			return nil, retryableFailure(attempt.Route.ProviderID)
		})
		require.Error(t, err)
		require.Equal(t, availability.StateOpen, tracker.Snapshot().Records[0].State)

		clock.Advance(time.Minute)
		tracker.Refresh(context.Background())
		result, err := executor.ExecuteChat(context.Background(), plan, func(_ context.Context, attempt routing.Attempt) (*inference.ChatResponse, *failure.Failure) {
			return &inference.ChatResponse{ID: "recovered", ModelUsed: attempt.Route.ID()}, nil
		})
		require.NoError(t, err)
		require.Equal(t, "recovered", result.Response.ID)
		require.Equal(t, availability.StateHealthy, tracker.Snapshot().Records[0].State)
	})
}

func testConfig() Config {
	return Config{
		MaxAttempts: 3, MaxRetriesPerRoute: 0, MaxElapsed: time.Minute,
		RetryBackoff: time.Second, BackoffMultiplier: 2, MaxBackoff: time.Minute,
	}
}

func newTestExecutor(t *testing.T, clock Clock, config Config) *Executor {
	t.Helper()
	executor, err := New(config, clock, nil)
	require.NoError(t, err)
	return executor
}

func testPlan(t *testing.T, routeIDs ...string) *routing.Plan {
	t.Helper()
	attempts := make([]routing.Attempt, len(routeIDs))
	for index, routeID := range routeIDs {
		parts := splitRoute(routeID)
		attempts[index].Route = routing.Route{
			CatalogGenerationID: "test-generation",
			ModelID:             parts[1],
			ProviderID:          parts[0],
			ProviderModelID:     parts[1],
		}
	}
	plan, err := routing.NewPlan("test-generation", 1, attempts, nil)
	require.NoError(t, err)
	return plan
}

func splitRoute(routeID string) [2]string {
	for index := range routeID {
		if routeID[index] == '/' {
			return [2]string{routeID[:index], routeID[index+1:]}
		}
	}
	return [2]string{"provider", routeID}
}

func retryableFailure(provider string) *failure.Failure {
	return failure.New(
		failure.ProviderUnavailable,
		"provider failed",
		true,
		failure.ProviderDetails{Provider: provider},
		errors.New("provider failed"),
	)
}

func evidenceStates(evidence []AttemptEvidence) []State {
	states := make([]State, len(evidence))
	for index := range evidence {
		states[index] = evidence[index].State
	}
	return states
}

func assertValidTransitions(t *testing.T, evidence []AttemptEvidence) {
	t.Helper()
	for _, attempt := range evidence {
		require.GreaterOrEqual(t, len(attempt.Transitions), 2)
		require.Equal(t, StateQueued, attempt.Transitions[0].To)
		require.Equal(t, StateRunning, attempt.Transitions[1].To)
		require.Contains(t, []State{StateSucceeded, StateFailed, StateCanceled}, attempt.State)
	}
}

type fakeExecutionClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeExecutionClock {
	return &fakeExecutionClock{now: time.Unix(100, 0)}
}

func (c *fakeExecutionClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeExecutionClock) Sleep(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Advance(duration)
	return nil
}

func (c *fakeExecutionClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type scriptedStream struct {
	position int
	events   []*inference.StreamEvent
	errors   []error
}

func (s *scriptedStream) Read() (*inference.StreamEvent, error) {
	position := s.position
	s.position++
	var event *inference.StreamEvent
	if position < len(s.events) {
		event = s.events[position]
	}
	if position < len(s.errors) && s.errors[position] != nil {
		return event, s.errors[position]
	}
	if event != nil {
		return event, nil
	}
	return nil, io.EOF
}

func (s *scriptedStream) Close() error { return nil }

type closeDrivenStream struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newCloseDrivenStream() *closeDrivenStream {
	return &closeDrivenStream{started: make(chan struct{}), closed: make(chan struct{})}
}

func (s *closeDrivenStream) Read() (*inference.StreamEvent, error) {
	s.once.Do(func() { close(s.started) })
	<-s.closed
	return nil, context.Canceled
}

func (s *closeDrivenStream) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}
