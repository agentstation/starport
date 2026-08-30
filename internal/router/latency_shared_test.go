package router

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeHealthStore is an in-memory stand-in for the deployment's distributed
// key-value store.
type fakeHealthStore struct {
	mu     sync.Mutex
	values map[string][]byte
	ttls   map[string]time.Duration
}

func newFakeHealthStore() *fakeHealthStore {
	return &fakeHealthStore{values: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (s *fakeHealthStore) SetWithTTL(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	s.ttls[key] = ttl
	return nil
}

func (s *fakeHealthStore) ScanWithPrefix(_ context.Context, prefix string, _ int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.values))
	for key := range s.values {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (s *fakeHealthStore) BatchGet(_ context.Context, keys []string) (map[string][]byte, error) {
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

// TestSharedLatencyTrackerConvergesPeers holds the fleet view: a replica that
// never called a provider reads the peer measurement from the shared store.
func TestSharedLatencyTrackerConvergesPeers(t *testing.T) {
	store := newFakeHealthStore()
	first := NewSharedLatencyTracker(NewLatencyTracker(0.2, 5), store)
	second := NewSharedLatencyTracker(NewLatencyTracker(0.2, 5), store)

	first.RecordLatency("openai", 120*time.Millisecond)
	require.Equal(t, 120*time.Millisecond, second.GetLatency("openai"),
		"a fresh replica starts with the fleet measurement")
	require.Equal(t, map[string]time.Duration{"openai": 120 * time.Millisecond},
		second.GetAllLatencies())

	for key, ttl := range store.ttls {
		require.True(t, strings.HasPrefix(key, sharedLatencyKeyPrefix))
		require.Equal(t, sharedLatencyTTL, ttl, "a replica snapshot must expire on its own")
	}
}

// TestSharedLatencyLocalMeasurementWins pins the merge rule: this replica's
// own measurement outranks every peer snapshot.
func TestSharedLatencyLocalMeasurementWins(t *testing.T) {
	store := newFakeHealthStore()
	first := NewSharedLatencyTracker(NewLatencyTracker(0.2, 5), store)
	second := NewSharedLatencyTracker(NewLatencyTracker(0.2, 5), store)

	first.RecordLatency("openai", 120*time.Millisecond)
	second.RecordLatency("openai", 40*time.Millisecond)
	require.Equal(t, 40*time.Millisecond, second.GetLatency("openai"))
	require.Equal(t, 40*time.Millisecond, second.GetAllLatencies()["openai"])

	second.Reset()
	require.Equal(t, 120*time.Millisecond, second.GetLatency("openai"),
		"a reset replica falls back to the fleet view")
}

// TestSharedHealthStoreOptionWrapsTheLatencyTracker holds the composition:
// the option turns the router's tracker into the shared one.
func TestSharedHealthStoreOptionWrapsTheLatencyTracker(t *testing.T) {
	registry := &mockRegistry{}
	shared := New(registry, WithSharedHealthStore(newFakeHealthStore())).(*modelRouter)
	_, ok := shared.latencyTracker.(*SharedLatencyTracker)
	require.True(t, ok, "a distributed store must wrap the latency tracker")

	local := New(registry).(*modelRouter)
	_, ok = local.latencyTracker.(*SharedLatencyTracker)
	require.False(t, ok, "without a distributed store the tracker stays process-local")
}
