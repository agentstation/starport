package availability

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/routing"
)

// fakeKVStore is an in-memory stand-in for the deployment's distributed
// key-value store.
type fakeKVStore struct {
	mu     sync.Mutex
	values map[string][]byte
	ttls   map[string]time.Duration
}

func newFakeKVStore() *fakeKVStore {
	return &fakeKVStore{values: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (s *fakeKVStore) SetWithTTL(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	s.ttls[key] = ttl
	return nil
}

func (s *fakeKVStore) ScanWithPrefix(_ context.Context, prefix string, _ int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.values))
	for key := range s.values {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (s *fakeKVStore) BatchGet(_ context.Context, keys []string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			values[key] = append([]byte(nil), value...)
		}
	}
	return values, nil
}

func sharedTestRoute() routing.Route {
	return routing.Route{
		CatalogGenerationID: "g1", ModelID: "author/primary",
		ProviderID: "provider", ProviderModelID: "primary-v1",
	}
}

func offeringFailure() *failure.Failure {
	return failure.New(
		failure.ProviderUnavailable,
		"failed",
		true,
		failure.ProviderDetails{StateScope: failure.ScopeOffering},
		nil,
	)
}

func sharedTracker(t *testing.T, clock Clock, store KVStore, instanceID string) *Tracker {
	t.Helper()
	tracker, err := New(DefaultConfig(), clock, nil)
	require.NoError(t, err)
	require.NoError(t, tracker.UseSharedStore(store, SharedConfig{InstanceID: instanceID}))
	return tracker
}

// TestSharedStoreConvergesTwoTrackers is the acceptance contract: a breaker
// one replica opens reaches the next replica within one refresh interval.
func TestSharedStoreConvergesTwoTrackers(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	store := newFakeKVStore()
	first := sharedTracker(t, clock, store, "replica-a")
	second := sharedTracker(t, clock, store, "replica-b")
	route := sharedTestRoute()

	for range 3 {
		first.RecordFailure(route, offeringFailure(), time.Second)
	}
	require.False(t, first.Acquire(route), "the failing replica opens its own breaker")
	require.True(t, second.Acquire(route), "the peer has not read shared state yet")

	second.Refresh(context.Background())
	require.False(t, second.Acquire(route), "the peer adopts the open breaker on refresh")
	record := second.Snapshot().Records[0]
	require.Equal(t, StateOpen, record.State)
	require.Equal(t, failure.ProviderUnavailable, record.FailureKind)

	require.Equal(t, DefaultSharedTTL, store.ttls[sharedHealthKeyPrefix+"replica-a"],
		"a replica record must expire on its own")
}

// TestLocalOnlyTrackerKeepsProcessState holds the fallback: without a shared
// store each replica learns failures alone, exactly as before this change.
func TestLocalOnlyTrackerKeepsProcessState(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	first, err := New(DefaultConfig(), clock, nil)
	require.NoError(t, err)
	second, err := New(DefaultConfig(), clock, nil)
	require.NoError(t, err)
	route := sharedTestRoute()

	for range 3 {
		first.RecordFailure(route, offeringFailure(), time.Second)
	}
	require.False(t, first.Acquire(route))

	second.Refresh(context.Background())
	require.True(t, second.Acquire(route), "a local-only tracker must not observe a peer breaker")
	require.Empty(t, second.Snapshot().Records)
}

// TestLocalStateWinsRecencyConflicts pins the merge rule: a local transition
// that is at least as recent as the peer evidence stands.
func TestLocalStateWinsRecencyConflicts(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	store := newFakeKVStore()
	first := sharedTracker(t, clock, store, "replica-a")
	second := sharedTracker(t, clock, store, "replica-b")
	route := sharedTestRoute()

	for range 3 {
		first.RecordFailure(route, offeringFailure(), time.Second)
	}

	// The peer proved the offering healthy after the breaker opened.
	second.RecordFailure(route, offeringFailure(), time.Second)
	clock.Advance(time.Second)
	second.RecordSuccess(route, time.Second)

	second.Refresh(context.Background())
	require.True(t, second.Acquire(route), "the newer local success outranks the older peer breaker")
	require.Equal(t, StateHealthy, second.Snapshot().Records[0].State)
}

// TestPeerReadsAreBoundedByTheRefreshInterval pins the read cost bound: a
// second refresh inside the interval reads nothing, and the one after the
// interval adopts the peer state.
func TestPeerReadsAreBoundedByTheRefreshInterval(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	store := newFakeKVStore()
	first := sharedTracker(t, clock, store, "replica-a")
	second := sharedTracker(t, clock, store, "replica-b")
	route := sharedTestRoute()

	second.Refresh(context.Background())
	for range 3 {
		first.RecordFailure(route, offeringFailure(), time.Second)
	}

	second.Refresh(context.Background())
	require.True(t, second.Acquire(route), "a refresh inside the interval must not read peers")

	clock.Advance(DefaultSharedRefreshInterval)
	second.Refresh(context.Background())
	require.False(t, second.Acquire(route), "the refresh after the interval adopts the peer breaker")
}

// TestUseSharedStoreRequiresAStore refuses a nil distributed store.
func TestUseSharedStoreRequiresAStore(t *testing.T) {
	tracker, err := New(DefaultConfig(), nil, nil)
	require.NoError(t, err)
	require.ErrorIs(t, tracker.UseSharedStore(nil, SharedConfig{}), ErrInvalidSharedStore)
}
