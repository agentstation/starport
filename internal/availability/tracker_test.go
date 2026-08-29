package availability

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestOfferingAvailabilityStateMachine(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	publisher := &capturePublisher{}
	tracker, err := New(Config{FailureThreshold: 2, OpenDuration: time.Minute}, clock, publisher)
	require.NoError(t, err)
	route := routing.Route{CatalogGenerationID: "g1", ModelID: "model", ProviderID: "provider", ProviderModelID: "model-v1"}
	retryable := failure.New(
		failure.ProviderUnavailable,
		"failed",
		true,
		failure.ProviderDetails{StateScope: failure.ScopeOffering},
		nil,
	)

	require.True(t, tracker.Acquire(route))
	tracker.RecordFailure(route, retryable, time.Second)
	require.True(t, tracker.Acquire(route))
	tracker.RecordFailure(route, retryable, time.Second)
	require.False(t, tracker.Acquire(route))
	require.Equal(t, StateOpen, tracker.Snapshot().Records[0].State)

	clock.Advance(time.Minute)
	tracker.Refresh(context.Background())
	require.True(t, tracker.Acquire(route))
	require.False(t, tracker.Acquire(route), "only one half-open probe is admitted")
	tracker.Release(route)
	require.True(t, tracker.Acquire(route), "a neutral release must make the half-open probe available")
	require.False(t, tracker.Acquire(route), "the replacement half-open probe remains exclusive")

	tracker.RecordSuccess(route, time.Second)
	require.True(t, tracker.Acquire(route))
	require.Equal(t, StateHealthy, tracker.Snapshot().Records[0].State)
	require.GreaterOrEqual(t, len(publisher.snapshots), 4)
}

func TestUnreachableOpensTheCircuit(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	tracker, err := New(Config{FailureThreshold: 2, OpenDuration: time.Minute}, clock, nil)
	require.NoError(t, err)
	route := routing.Route{CatalogGenerationID: "g1", ModelID: "model", ProviderID: "provider", ProviderModelID: "model-v1"}
	unreachable := failure.New(
		failure.Unreachable,
		"no response",
		true,
		failure.ProviderDetails{StateScope: failure.ScopeOffering},
		nil,
	)

	// A provider that never answers must trip the same breaker a responding
	// erroring provider does, and the record must keep the no-response kind
	// so the projection can tell the two apart.
	tracker.RecordFailure(route, unreachable, time.Second)
	tracker.RecordFailure(route, unreachable, time.Second)
	record := tracker.Snapshot().Records[0]
	require.Equal(t, StateOpen, record.State)
	require.Equal(t, failure.Unreachable, record.FailureKind)
}

func TestNotFoundRequiresExplicitReset(t *testing.T) {
	tracker, err := New(DefaultConfig(), nil, nil)
	require.NoError(t, err)
	route := routing.Route{CatalogGenerationID: "g1", ModelID: "model", ProviderID: "provider", ProviderModelID: "missing"}
	notFound := failure.New(
		failure.NotFound,
		"not found",
		false,
		failure.ProviderDetails{StateScope: failure.ScopeOffering},
		nil,
	)

	tracker.RecordFailure(route, notFound, 0)
	require.False(t, tracker.Acquire(route))
	require.Equal(t, StateUnavailable, tracker.Snapshot().Records[0].State)
	require.NoError(t, tracker.Reset(OfferingFromRoute(route)))
	require.True(t, tracker.Acquire(route))
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type capturePublisher struct {
	mu        sync.Mutex
	snapshots []Snapshot
}

func (p *capturePublisher) PublishAvailability(snapshot Snapshot) error {
	p.mu.Lock()
	p.snapshots = append(p.snapshots, snapshot)
	p.mu.Unlock()
	return nil
}
