package cache

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConcurrentOptionalCacheCapacity(t *testing.T) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	config := ManagerConfig{}
	config.Responses.LocalSizeMB = 2
	config.Models.SizeMB = 2
	manager, err := NewCacheManager(config, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	extraction, err := NewBufferedLocalCache(2, time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, extraction.Close()) })
	payload := []byte(strings.Repeat("x", 8<<10))
	model := struct{ Text string }{Text: string(payload)}
	errors := make(chan error, 32)
	var workers sync.WaitGroup
	for worker := range 32 {
		workers.Go(func() {
			for entry := range 128 {
				key := fmt.Sprintf("%d/%d", worker, entry)
				if err := manager.SetResponse(t.Context(), key, payload); err != nil {
					errors <- err
					return
				}
				if err := manager.SetModel(t.Context(), key, model); err != nil {
					errors <- err
					return
				}
				if err := extraction.Set(t.Context(), key, payload, time.Minute); err != nil {
					errors <- err
					return
				}
				for _, queue := range []*fillQueue{manager.fills, extraction.fills} {
					stats := queue.stats()
					if stats.RetainedBytes > fillQueueBytes || stats.RetainedEntries > fillQueueEntries || stats.ActiveFills > fillWorkers {
						errors <- fmt.Errorf("fill capacity exceeded: %+v", stats)
						return
					}
				}
			}
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	for _, queue := range []*fillQueue{manager.fills, extraction.fills} {
		stats := queue.stats()
		require.LessOrEqual(t, stats.RetainedBytes, int64(fillQueueBytes))
		require.LessOrEqual(t, stats.RetainedEntries, int64(fillQueueEntries))
		require.LessOrEqual(t, stats.ActiveFills, int64(fillWorkers))
		require.Eventually(t, func() bool { return queue.stats().RetainedEntries == 0 }, 5*time.Second, time.Millisecond)
		require.Positive(t, queue.stats().CompletedFills)
	}
	for _, store := range []*LocalCache{manager.responses.(*LocalCache), manager.models, extraction.local} {
		metrics := store.cache.Metrics
		cost := metrics.CostAdded() - metrics.CostEvicted()
		require.LessOrEqual(t, cost, uint64(2<<20))
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("32 callers, 4096 keys per cache: retained heap delta=%d bytes; total allocations=%d bytes; response/model drops=%d; extraction drops=%d",
		int64(after.HeapAlloc)-int64(before.HeapAlloc), after.TotalAlloc-before.TotalAlloc,
		manager.fills.stats().DroppedFills, extraction.fills.stats().DroppedFills)
	runtime.KeepAlive(manager)
	runtime.KeepAlive(extraction)
	require.NoError(t, manager.Close())
	require.NoError(t, extraction.Close())
	for _, queue := range []*fillQueue{manager.fills, extraction.fills} {
		require.Zero(t, queue.stats().RetainedBytes)
		require.Zero(t, queue.stats().ActiveFills)
	}
}
