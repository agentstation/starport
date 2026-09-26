package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/availability"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

type advisoryFaultStore struct {
	mu               sync.Mutex
	values           map[string][]byte
	healthBlocked    chan struct{}
	latencyPublished chan struct{}
}

func (s *advisoryFaultStore) SetWithTTL(ctx context.Context, key string, value []byte, _ time.Duration) error {
	if strings.HasPrefix(key, "provider-health:") {
		select {
		case s.healthBlocked <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	s.mu.Lock()
	s.values[key] = append([]byte(nil), value...)
	s.mu.Unlock()
	select {
	case s.latencyPublished <- struct{}{}:
	default:
	}
	return nil
}
func (s *advisoryFaultStore) ScanWithPrefix(ctx context.Context, prefix string, _ int) ([]string, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for key := range s.values {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}
func (s *advisoryFaultStore) BatchGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make(map[string][]byte, len(keys))
	for _, key := range keys {
		values[key] = append([]byte(nil), s.values[key]...)
	}
	return values, nil
}

func TestAdvisoryFleetFaultIsolation(t *testing.T) {
	store := &advisoryFaultStore{values: make(map[string][]byte), healthBlocked: make(chan struct{}, 1), latencyPublished: make(chan struct{}, 1)}
	health, err := availability.New(availability.DefaultConfig(), nil, nil)
	require.NoError(t, err)
	require.NoError(t, health.UseSharedStore(store, availability.SharedConfig{}))
	latency := router.NewSharedLatencyTracker(nil, store)
	owner := &advisoryWorkers{workers: []func(context.Context){health.RunShared, latency.Run}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner.Start(ctx)
	defer func() { require.NoError(t, owner.Close(t.Context())) }()
	select {
	case <-store.healthBlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("health publication did not block")
	}
	select {
	case <-store.latencyPublished:
	case <-time.After(5 * time.Second):
		t.Fatal("health failure stalled the independent latency worker")
	}
	route := routing.Route{ProviderID: "provider", ProviderModelID: "model"}
	providerFailure := failure.New(failure.ProviderUnavailable, "unavailable", true, failure.ProviderDetails{StateScope: failure.ScopeOffering}, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			health.RecordFailure(route, providerFailure, time.Millisecond)
			health.Refresh(ctx)
			latency.RecordLatency("provider", time.Millisecond)
			latency.GetLatency("cold")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("callback pressure stalled behind shared storage")
	}
	require.False(t, health.Acquire(route), "local breaker transitions remain immediate")
	require.Equal(t, time.Millisecond, latency.GetLatency("provider"))
	cancel()
	shutdown, cancelShutdown := context.WithTimeout(t.Context(), time.Second)
	defer cancelShutdown()
	require.NoError(t, owner.Close(shutdown))
}
