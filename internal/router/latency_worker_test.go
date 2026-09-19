package router

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLatencyWorkerDoesNotBlockCallbacks(t *testing.T) {
	store := &blockedAdvisoryStore{entered: make(chan string, 1), release: make(chan struct{})}
	tracker := NewSharedLatencyTracker(NewLatencyTracker(0.2, 5), store)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); tracker.Run(ctx) }()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach storage")
	}
	// A second lifecycle call cannot create another exchange worker.
	duplicateDone := make(chan struct{})
	go func() { tracker.Run(ctx); close(duplicateDone) }()
	select {
	case <-duplicateDone:
	case <-time.After(5 * time.Second):
		t.Fatal("duplicate worker did not return")
	}
	tracker.RecordLatency("local", 7*time.Millisecond)
	require.Equal(t, 7*time.Millisecond, tracker.GetLatency("local"))
	require.Zero(t, tracker.GetLatency("cold"))
	require.Equal(t, 7*time.Millisecond, tracker.GetAllLatencies()["local"])
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not cancel its storage call")
	}
	require.False(t, tracker.running.Load())
}

func TestLatencyPeerValidity(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		observed time.Time
		millis   int64
		accepted bool
	}{
		{"fresh", now, 12, true},
		{"expired", now.Add(-sharedLatencyTTL), 12, false},
		{"future", now.Add(time.Hour), 12, false},
		{"negative", now, -1, false},
		{"overflow", now, 1<<63 - 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeHealthStore()
			data, err := json.Marshal(sharedLatencyDocument{InstanceID: "peer", UpdatedAt: tc.observed, Latencies: map[string]int64{"provider": tc.millis}})
			require.NoError(t, err)
			require.NoError(t, store.SetWithTTL(t.Context(), sharedLatencyKeyPrefix+"peer", data, sharedLatencyTTL))
			tracker := NewSharedLatencyTracker(NewLatencyTracker(0.2, 5), store)
			tracker.refreshPeers(t.Context())
			if tc.accepted {
				require.Equal(t, 12*time.Millisecond, tracker.GetLatency("provider"))
			} else {
				require.Zero(t, tracker.GetLatency("provider"))
			}
		})
	}
}

func TestLatencyCachedHintExpiresWithoutStorage(t *testing.T) {
	tracker := NewSharedLatencyTracker(NewLatencyTracker(0.2, 5), newFakeHealthStore())
	tracker.peers["provider"] = peerLatency{value: time.Millisecond, observed: time.Now().Add(-sharedLatencyTTL)}
	require.Zero(t, tracker.GetLatency("provider"))
	require.Empty(t, tracker.GetAllLatencies())
}

func TestLatencyEmptyPeerScanClearsOldHints(t *testing.T) {
	tracker := NewSharedLatencyTracker(NewLatencyTracker(0.2, 5), newFakeHealthStore())
	tracker.peers["provider"] = peerLatency{value: time.Millisecond, observed: time.Now()}
	tracker.refreshPeers(t.Context())
	require.Zero(t, tracker.GetLatency("provider"))
}

type recoveringAdvisoryStore struct {
	*fakeHealthStore
	unavailable atomic.Bool
}

func (s *recoveringAdvisoryStore) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s.unavailable.Load() {
		return errors.New("store unavailable")
	}
	return s.fakeHealthStore.SetWithTTL(ctx, key, value, ttl)
}

func (s *recoveringAdvisoryStore) ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error) {
	if s.unavailable.Load() {
		return nil, errors.New("store unavailable")
	}
	return s.fakeHealthStore.ScanWithPrefix(ctx, prefix, limit)
}

func TestLatencyWorkerRecoversAfterOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &recoveringAdvisoryStore{fakeHealthStore: newFakeHealthStore()}
		peer := NewSharedLatencyTracker(nil, store)
		peer.RecordLatency("provider", 17*time.Millisecond)
		peer.exchange(t.Context())
		store.unavailable.Store(true)
		tracker := NewSharedLatencyTracker(nil, store)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() { defer close(done); tracker.Run(ctx) }()
		synctest.Wait()
		require.Zero(t, tracker.GetLatency("provider"))
		store.unavailable.Store(false)
		time.Sleep(sharedLatencyRefreshInterval)
		synctest.Wait()
		require.Equal(t, 17*time.Millisecond, tracker.GetLatency("provider"))
		cancel()
		<-done
	})
}
