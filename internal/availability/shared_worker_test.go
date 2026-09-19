package availability

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealthWorkerCancelsBlockedStorage(t *testing.T) {
	store := &blockedAdvisoryStore{entered: make(chan string, 1), release: make(chan struct{})}
	tracker := sharedTracker(t, nil, store, "worker")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); tracker.RunShared(ctx) }()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach storage")
	}
	route := sharedTestRoute()
	for range 3 {
		tracker.RecordFailure(route, offeringFailure(), time.Second)
	}
	require.False(t, tracker.Acquire(route), "local breaker must open during a storage outage")
	tracker.Refresh(t.Context())
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not cancel storage")
	}
	require.False(t, tracker.sharedRunning.Load())
}

func TestPeerHealthExpiresWithoutRepublishing(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	store := newFakeKVStore()
	first := sharedTracker(t, clock, store, "first")
	second := sharedTracker(t, clock, store, "second")
	route := sharedTestRoute()
	for range 3 {
		first.RecordFailure(route, offeringFailure(), time.Second)
	}
	first.exchangeShared(t.Context())
	second.exchangeShared(t.Context())
	require.False(t, second.Acquire(route))
	second.mu.Lock()
	require.Empty(t, second.sharedDocumentLocked().Records, "adopted observations must not become local evidence")
	second.mu.Unlock()
	clock.Advance(DefaultSharedTTL)
	second.Refresh(t.Context())
	require.True(t, second.Acquire(route), "an expired peer hint cannot keep an offering closed")
	require.Empty(t, second.Snapshot().Records)
}

func TestPeerExpiryRestoresLocalEvidence(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	store := newFakeKVStore()
	local := sharedTracker(t, clock, store, "local")
	remote := sharedTracker(t, clock, store, "remote")
	route := sharedTestRoute()
	local.RecordFailure(route, offeringFailure(), time.Second)
	local.RecordSuccess(route, time.Second)
	clock.Advance(time.Second)
	for range 3 {
		remote.RecordFailure(route, offeringFailure(), time.Second)
	}
	remote.exchangeShared(t.Context())
	local.exchangeShared(t.Context())
	require.False(t, local.Acquire(route))
	clock.Advance(DefaultSharedTTL)
	local.Refresh(t.Context())
	require.True(t, local.Acquire(route))
	require.Equal(t, StateHealthy, local.Snapshot().Records[0].State)
}

func TestLocalResetRejectsOlderPeerEvidence(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	tracker := sharedTracker(t, clock, newFakeKVStore(), "local")
	route := sharedTestRoute()
	tracker.RecordFailure(route, offeringFailure(), time.Second)
	clock.Advance(time.Second)
	require.NoError(t, tracker.Reset(OfferingFromRoute(route)))
	tracker.mu.Lock()
	accepted := tracker.adoptRecordLocked(sharedRecord{
		ProviderID: route.ProviderID, ProviderModelID: route.ProviderModelID,
		State: StateUnavailable, UpdatedAt: time.Unix(100, 0),
	})
	tracker.mu.Unlock()
	require.False(t, accepted, "an operator reset outranks older peer evidence")
	require.True(t, tracker.Acquire(route))
}

func TestPeerExpiryPublishesFromAdmission(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	publisher := &capturePublisher{}
	tracker, err := New(DefaultConfig(), clock, publisher)
	require.NoError(t, err)
	route := sharedTestRoute()
	offering := OfferingFromRoute(route)
	tracker.records[offering] = &entry{state: StateUnavailable, updatedAt: clock.Now()}
	tracker.peerExpires[offering] = clock.Now().Add(time.Second)
	clock.Advance(time.Second)
	require.True(t, tracker.Acquire(route))
	require.Len(t, publisher.snapshots, 1)
	require.Empty(t, publisher.snapshots[0].Records, "expiry must also repair the routable projection")
}

type recoveringAdvisoryStore struct {
	*fakeKVStore
	unavailable atomic.Bool
}

func (s *recoveringAdvisoryStore) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s.unavailable.Load() {
		return errors.New("store unavailable")
	}
	return s.fakeKVStore.SetWithTTL(ctx, key, value, ttl)
}

func (s *recoveringAdvisoryStore) ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error) {
	if s.unavailable.Load() {
		return nil, errors.New("store unavailable")
	}
	return s.fakeKVStore.ScanWithPrefix(ctx, prefix, limit)
}

func TestHealthWorkerRecoversAfterOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &recoveringAdvisoryStore{fakeKVStore: newFakeKVStore()}
		peer := sharedTracker(t, nil, store, "peer")
		route := sharedTestRoute()
		for range 3 {
			peer.RecordFailure(route, offeringFailure(), time.Second)
		}
		peer.exchangeShared(t.Context())
		store.unavailable.Store(true)
		tracker := sharedTracker(t, nil, store, "local")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() { defer close(done); tracker.RunShared(ctx) }()
		synctest.Wait()
		require.True(t, tracker.Acquire(route))
		store.unavailable.Store(false)
		time.Sleep(DefaultSharedRefreshInterval)
		synctest.Wait()
		require.False(t, tracker.Acquire(route))
		cancel()
		<-done
	})
}
