package authorization

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestAuthorizationTenantChurnCapacity(t *testing.T) {
	if os.Getenv("TEST_AUTHORIZATION_CAPACITY") != "1" {
		t.Skip("UNVERIFIED: set TEST_AUTHORIZATION_CAPACITY=1 for the capacity profile")
	}
	for _, width := range []int{2048, 8192} {
		t.Run(fmt.Sprintf("nested_values_%d", width), func(t *testing.T) {
			now := time.Now()
			values := make([]any, width)
			for i := range values {
				values[i] = []any{}
			}
			limits := CacheLimits{Entries: 1024, Bytes: 16 << 20, BundleBytes: 64 << 10, ConcurrentLoads: 16, TenantLoads: 4, LoadTimeout: time.Second, PermissionLifetime: time.Minute}
			cache := newTestCache(t, sourceFunc(func(_ context.Context, id Identity) (Candidate, error) {
				candidate := cacheCandidate(id, now)
				candidate.Key.APIKey.Metadata = map[string]any{"values": values}
				return candidate, nil
			}), limits, func() (time.Time, bool) { return now, true })
			sample := func() runtime.MemStats {
				cache.work.Wait()
				runtime.GC()
				runtime.GC()
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				runtime.KeepAlive(cache)
				return stats
			}
			baseline := sample()
			var previous runtime.MemStats
			for sweep := range 4 {
				var sampledHighWater uint64
				for offset := 0; offset < limits.Entries; offset += limits.ConcurrentLoads {
					var workers sync.WaitGroup
					failures := make(chan error, limits.ConcurrentLoads)
					for index := offset; index < min(offset+limits.ConcurrentLoads, limits.Entries); index++ {
						workers.Go(func() {
							id := Identity{Tenant: fmt.Sprintf("tenant-%d", sweep*limits.Entries+index), Subject: fmt.Sprintf("key-%d", sweep*limits.Entries+index)}
							if _, err := cache.Resolve(t.Context(), id); err != nil {
								failures <- err
								return
							}
							cache.mu.Lock()
							valid := cache.resident <= limits.Entries && cache.bytes <= limits.Bytes && len(cache.entries) <= limits.Entries+limits.ConcurrentLoads
							cache.mu.Unlock()
							if !valid {
								failures <- fmt.Errorf("tenant churn exceeded the resident entry, encoded byte, or in-flight bound")
							}
						})
					}
					workers.Wait()
					close(failures)
					for err := range failures {
						t.Error(err)
					}
					if t.Failed() {
						t.FailNow()
					}
					var transient runtime.MemStats
					runtime.ReadMemStats(&transient)
					sampledHighWater = max(sampledHighWater, transient.HeapAlloc)
				}
				stats := sample()
				// The isolated cache cannot exceed the product's whole-replica live-heap budget.
				if stats.HeapAlloc > 1<<30 {
					t.Fatalf("isolated authorization heap = %d bytes, above 1 GiB", stats.HeapAlloc)
				}
				cache.mu.Lock()
				resident, encoded := cache.resident, cache.bytes
				cache.mu.Unlock()
				allocated := stats.TotalAlloc - baseline.TotalAlloc
				if sweep > 0 {
					allocated = stats.TotalAlloc - previous.TotalAlloc
				}
				t.Logf("capacity sweep=%d tenants=%d resident=%d encoded_bytes=%d live_heap_bytes=%d heap_delta_bytes=%d allocated_bytes_in_sweep=%d sampled_heap_high_water_bytes=%d", sweep+1, (sweep+1)*limits.Entries, resident, encoded, stats.HeapAlloc, int64(stats.HeapAlloc)-int64(baseline.HeapAlloc), allocated, sampledHighWater)
				previous = stats
			}
			cache.Close()
			released := sample()
			cache.mu.Lock()
			empty := len(cache.entries) == 0 && cache.resident == 0 && cache.bytes == 0 && cache.active == 0 && len(cache.tenants) == 0
			cache.mu.Unlock()
			if !empty {
				t.Fatal("shutdown retained authorization records or load ownership")
			}
			t.Logf("capacity closed live_heap_bytes=%d heap_delta_bytes=%d total_allocated_bytes=%d", released.HeapAlloc, int64(released.HeapAlloc)-int64(baseline.HeapAlloc), released.TotalAlloc-baseline.TotalAlloc)
		})
	}
}
