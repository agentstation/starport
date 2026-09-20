package cache

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultCacheMemoryBudget(t *testing.T) {
	if os.Getenv("TEST_DEFAULT_CACHE_CAPACITY") != "1" {
		t.Skip("UNVERIFIED: set TEST_DEFAULT_CACHE_CAPACITY=1 for default-capacity qualification")
	}
	runtime.GC()
	var baseline, full, churned, closed runtime.MemStats
	runtime.ReadMemStats(&baseline)
	manager, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	extraction, err := NewBufferedLocalCache(16, time.Hour)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, extraction.Close()) })
	payload := []byte(strings.Repeat("x", 256<<10))
	model := struct{ Text string }{Text: string(payload)}
	drain := func() {
		t.Helper()
		require.Eventually(t, func() bool {
			return manager.fills.stats().RetainedEntries == 0 && extraction.fills.stats().RetainedEntries == 0
		}, 5*time.Second, time.Millisecond)
	}
	stores := []*LocalCache{manager.responses.(*LocalCache), manager.models, extraction.local}
	budgets := []uint64{256 << 20, 16 << 20, 16 << 20}
	fill := func(round int) {
		for index := range 1536 {
			key := fmt.Sprintf("%d/%d", round, index)
			require.NoError(t, manager.SetResponse(t.Context(), key, payload))
			if index < 128 {
				require.NoError(t, manager.SetModel(t.Context(), key, model))
				require.NoError(t, extraction.Set(t.Context(), key, payload, time.Hour))
			}
			if index%4 == 3 {
				drain()
			}
		}
		drain()
		for index, store := range stores {
			metrics := store.cache.Metrics
			cost := metrics.CostAdded() - metrics.CostEvicted()
			require.Greater(t, cost, budgets[index]*3/4, "fixture must substantially occupy every default budget")
			require.LessOrEqual(t, cost, budgets[index])
		}
	}
	fill(0)
	runtime.GC()
	runtime.ReadMemStats(&full)
	fill(1)
	runtime.GC()
	runtime.ReadMemStats(&churned)
	require.Less(t, churned.HeapAlloc-baseline.HeapAlloc, uint64(1<<30), "optional caches alone must fit the whole-replica live-heap target")
	t.Logf("default budgets: filled heap delta=%d churned heap delta=%d churn allocations=%d", int64(full.HeapAlloc)-int64(baseline.HeapAlloc), int64(churned.HeapAlloc)-int64(baseline.HeapAlloc), churned.TotalAlloc-full.TotalAlloc)
	runtime.KeepAlive(stores)
	require.NoError(t, manager.Close())
	require.NoError(t, extraction.Close())
	runtime.GC()
	runtime.ReadMemStats(&closed)
	t.Logf("after close heap delta=%d", int64(closed.HeapAlloc)-int64(baseline.HeapAlloc))
	require.Less(t, int64(closed.HeapAlloc)-int64(baseline.HeapAlloc), int64(32<<20), "closed caches must release retained payloads")
	runtime.KeepAlive(stores)
}
