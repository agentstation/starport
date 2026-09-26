package cache

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

// This test pauses the selected service. Use a disposable cache instance.
func TestSharedCachePausedServiceDeadlineAndRecovery(t *testing.T) {
	raw := os.Getenv("TEST_SHARED_CACHE_FAULT_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_SHARED_CACHE_FAULT_URL is not set")
	}
	options, err := valkey.ParseURL(raw)
	require.NoError(t, err)
	options.DisableCache = true
	control, err := valkey.NewClient(options)
	require.NoError(t, err)
	defer control.Close()
	store, err := OpenShared(SharedConfig{URL: raw, DeploymentID: "outage-" + rand.Text(), AllowInsecure: true})
	require.NoError(t, err)
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, store)
	require.NoError(t, err)
	defer manager.Close()
	require.Eventually(t, func() bool { return store.SharedStatus().Available }, 5*time.Second, time.Millisecond)
	require.NoError(t, store.Set(t.Context(), "warm", []byte("retained"), time.Minute))
	value, found, err := manager.GetResponse(t.Context(), "warm")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "retained", string(value))

	pause, cancel := context.WithTimeout(t.Context(), time.Second)
	require.NoError(t, control.Do(pause, control.B().Arbitrary("CLIENT", "PAUSE", "1000", "ALL").Build()).Error())
	cancel()
	type sample struct {
		elapsed time.Duration
		err     error
	}
	results := make(chan sample, 32)
	for caller := range 32 {
		go func() {
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, found, err := manager.GetResponse(ctx, "warm")
			if err == nil || found {
				results <- sample{err: fmt.Errorf("paused service unexpectedly answered: found=%v err=%v", found, err)}
				return
			}
			if err := ctx.Err(); err != nil {
				results <- sample{err: fmt.Errorf("cache consumed caller deadline: %w", err)}
				return
			}
			if err := manager.SetResponse(ctx, fmt.Sprintf("fill-%d", caller), []byte("optional")); err != nil {
				results <- sample{err: err}
				return
			}
			results <- sample{elapsed: time.Since(start)}
		}()
	}
	durations := make([]time.Duration, 0, 32)
	for range 32 {
		result := <-results
		require.NoError(t, result.err)
		durations = append(durations, result.elapsed)
	}
	slices.Sort(durations)
	t.Logf("paused service: 32 concurrent read+fill calls, p50=%s p95=%s max=%s", durations[15], durations[30], durations[31])
	require.Eventually(t, func() bool {
		value, found, err := manager.GetResponse(t.Context(), "warm")
		return err == nil && found && string(value) == "retained" && store.SharedStatus().Available
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, manager.Close())
	require.Zero(t, manager.FillStatus().ActiveFills)
	require.Zero(t, manager.FillStatus().RetainedEntries)
}
