package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"
)

// waitForState blocks until one operation reaches a state, or the test fails.
func waitForState(
	t *testing.T,
	registry *Operations,
	id string,
	want OperationState,
) Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		operation, err := registry.Get(id)
		require.NoError(t, err)
		if operation.State == want {
			return operation
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s stayed %q, wanted %q", id, operation.State, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestOverlappingRefreshesJoinOneRun proves the registry keeps one open
// operation per kind. A second request receives the identifier of the run in
// flight, and the work runs once.
func TestOverlappingRefreshesJoinOneRun(t *testing.T) {
	registry := NewOperations()
	t.Cleanup(registry.Close)

	release := make(chan struct{})
	var runs int
	var mu sync.Mutex
	work := func(context.Context) (OperationResult, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		<-release
		return OperationResult{GenerationID: "gen-2", Changed: true}, nil
	}

	first, joined := registry.Submit(KindCatalogUpdate, work)
	require.False(t, joined)
	require.NotEmpty(t, first.ID)
	waitForState(t, registry, first.ID, OperationRunning)

	second, joinedSecond := registry.Submit(KindCatalogUpdate, work)
	assert.True(t, joinedSecond, "the second request joins the run in flight")
	assert.Equal(t, first.ID, second.ID)

	close(release)
	closed := waitForState(t, registry, first.ID, OperationSucceeded)
	assert.Equal(t, "gen-2", closed.GenerationID)
	assert.True(t, closed.Changed)
	assert.Equal(t, ReasonNone, closed.Reason)
	mu.Lock()
	assert.Equal(t, 1, runs, "one run serves both requests")
	mu.Unlock()

	// A closed operation frees the kind, so the next request starts its own
	// run rather than joining the one that ended.
	third, joinedThird := registry.Submit(
		KindCatalogUpdate,
		func(context.Context) (OperationResult, error) { return OperationResult{}, nil },
	)
	assert.False(t, joinedThird)
	assert.NotEqual(t, first.ID, third.ID)
	waitForState(t, registry, third.ID, OperationSucceeded)
}

// TestOperationsCloseTerminalStates walks every way one operation ends.
func TestOperationsCloseTerminalStates(t *testing.T) {
	tests := []struct {
		name    string
		work    OperationWork
		cancel  bool
		state   OperationState
		reason  OperationReason
		changed bool
	}{
		{
			name: "the work succeeded",
			work: func(context.Context) (OperationResult, error) {
				return OperationResult{GenerationID: "gen-3", Changed: true}, nil
			},
			state:   OperationSucceeded,
			reason:  ReasonNone,
			changed: true,
		},
		{
			name: "the source answered nothing usable",
			work: func(context.Context) (OperationResult, error) {
				return OperationResult{}, &starmaperrors.SyncError{
					Provider: "starmap",
					Err:      errors.New("https://catalog.internal.example refused"),
				}
			},
			state:  OperationFailed,
			reason: ReasonSourceUnavailable,
		},
		{
			name: "the candidate did not become routable",
			work: func(context.Context) (OperationResult, error) {
				return OperationResult{}, fmt.Errorf(
					"%w: the route table is empty", ErrRouteValidationFailed,
				)
			},
			state:  OperationFailed,
			reason: ReasonRouteValidationFailed,
		},
		{
			name: "the instance lost the lease",
			work: func(context.Context) (OperationResult, error) {
				return OperationResult{}, ErrStaleLeaseEpoch
			},
			state:  OperationFailed,
			reason: ReasonStaleLeaseEpoch,
		},
		{
			name: "an operator ended the run",
			work: func(ctx context.Context) (OperationResult, error) {
				<-ctx.Done()
				return OperationResult{}, ctx.Err()
			},
			cancel: true,
			state:  OperationCanceled,
			reason: ReasonCanceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewOperations()
			t.Cleanup(registry.Close)

			operation, joined := registry.Submit(KindCatalogUpdate, test.work)
			require.False(t, joined)
			if test.cancel {
				waitForState(t, registry, operation.ID, OperationRunning)
				canceled, err := registry.Cancel(operation.ID)
				require.NoError(t, err)
				assert.Equal(t, operation.ID, canceled.ID)
			}
			closed := waitForState(t, registry, operation.ID, test.state)
			assert.Equal(t, test.reason, closed.Reason)
			assert.Equal(t, test.changed, closed.Changed)
			assert.False(t, closed.Open())
			assert.False(t, closed.CompletedAt.IsZero())

			// A repeated cancel answers the terminal state and changes
			// nothing.
			again, err := registry.Cancel(operation.ID)
			require.NoError(t, err)
			assert.Equal(t, test.state, again.State)
			assert.Equal(t, test.reason, again.Reason)
		})
	}
}

// TestOperationsBoundTheHistory proves the registry keeps a bounded history,
// newest first, and answers an identifier it does not hold with the typed
// refusal.
func TestOperationsBoundTheHistory(t *testing.T) {
	registry := NewOperations(WithRetainedOperations(2))
	t.Cleanup(registry.Close)

	ids := make([]string, 0, 4)
	for index := range 4 {
		operation, _ := registry.Submit(
			KindCatalogUpdate,
			func(context.Context) (OperationResult, error) {
				return OperationResult{GenerationID: fmt.Sprintf("gen-%d", index)}, nil
			},
		)
		waitForState(t, registry, operation.ID, OperationSucceeded)
		ids = append(ids, operation.ID)
	}

	held := registry.List()
	require.Len(t, held, 2)
	assert.Equal(t, ids[3], held[0].ID, "the newest operation comes first")
	assert.Equal(t, ids[2], held[1].ID)

	_, err := registry.Get(ids[0])
	require.ErrorIs(t, err, ErrOperationNotFound)
	assert.NotContains(t, err.Error(), "gen-0")
}

// TestOperationTimeoutClosesTheRun proves a wedged run reaches its bound and
// closes as a timed-out operation.
func TestOperationTimeoutClosesTheRun(t *testing.T) {
	registry := NewOperations(WithOperationTimeout(20 * time.Millisecond))
	t.Cleanup(registry.Close)

	operation, _ := registry.Submit(
		KindCatalogUpdate,
		func(ctx context.Context) (OperationResult, error) {
			<-ctx.Done()
			return OperationResult{}, ctx.Err()
		},
	)
	closed := waitForState(t, registry, operation.ID, OperationFailed)
	assert.Equal(t, ReasonTimedOut, closed.Reason)
}

// TestClassifyOperationFailureNamesASafeCause proves every failure reads as a
// value from the closed set, and that no failure text reaches the reason.
func TestClassifyOperationFailureNamesASafeCause(t *testing.T) {
	secret := "https://catalog.internal.example?token=redact-me"
	tests := []struct {
		name    string
		failure error
		want    OperationReason
	}{
		{name: "no failure", failure: nil, want: ReasonNone},
		{
			name:    "a deadline",
			failure: fmt.Errorf("read %s: %w", secret, context.DeadlineExceeded),
			want:    ReasonTimedOut,
		},
		{
			name:    "a cancellation",
			failure: fmt.Errorf("read %s: %w", secret, context.Canceled),
			want:    ReasonCanceled,
		},
		{
			name:    "a stale lease epoch",
			failure: fmt.Errorf("accept: %w", ErrStaleLeaseEpoch),
			want:    ReasonStaleLeaseEpoch,
		},
		{
			name:    "a route validation failure",
			failure: fmt.Errorf("%w: %s", ErrRouteValidationFailed, secret),
			want:    ReasonRouteValidationFailed,
		},
		{
			name:    "no catalog",
			failure: ErrCatalogRequired,
			want:    ReasonCatalogUnavailable,
		},
		{
			name:    "no catalog source",
			failure: ErrCatalogSourceRequired,
			want:    ReasonCatalogUnavailable,
		},
		{
			name:    "another instance moved the head first",
			failure: &starmaperrors.ConflictError{Resource: "generation", Message: secret},
			want:    ReasonAcceptedHeadConflict,
		},
		{
			name:    "the source holds no generation",
			failure: &starmaperrors.NotFoundError{Resource: "generation", ID: secret},
			want:    ReasonCatalogUnavailable,
		},
		{
			name: "the source authentication failed",
			failure: &starmaperrors.AuthenticationError{
				Provider: "starmap",
				Message:  secret,
			},
			want: ReasonSourceUnavailable,
		},
		{
			name:    "a cause the set does not name",
			failure: errors.New(secret),
			want:    ReasonInternalError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason := ClassifyOperationFailure(test.failure)
			assert.Equal(t, test.want, reason)
			assert.NotContains(t, string(reason), "redact-me")
		})
	}
}
