package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/document"
	"github.com/stretchr/testify/require"
)

// These component measurements include serialization and optional fill admission.
// They exclude HTTP, authentication, providers, and shared-cache transport.
func BenchmarkOptionalCacheWork(b *testing.B) {
	for _, size := range []int{1024, 256 << 10, 4 << 20} {
		b.Run(fmt.Sprintf("model-fill/%d", size), func(b *testing.B) {
			manager, err := cache.NewCacheManager(cache.ManagerConfig{}, nil)
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, manager.Close()) })
			model := map[string]string{"description": strings.Repeat("x", size)}
			b.ReportAllocs()
			for b.Loop() {
				require.NoError(b, manager.SetModel(b.Context(), "model", model))
			}
			b.ReportMetric(float64(manager.FillStatus().DroppedFills)/float64(b.N), "drops/op")
		})
		b.Run(fmt.Sprintf("extraction-fill/%d", size), func(b *testing.B) {
			store, err := cache.NewBufferedLocalCache(16, document.DefaultCacheWindow)
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, store.Close()) })
			extractions, err := document.NewCache(store, nil, 0)
			require.NoError(b, err)
			key := document.CacheKey{AccountID: "account", ContentHash: "hash", Engine: "native", Generation: "generation"}
			reading := document.Reading{Text: strings.Repeat("x", size)}
			b.ReportAllocs()
			for b.Loop() {
				require.NoError(b, extractions.Put(b.Context(), key, reading))
			}
			b.ReportMetric(float64(store.FillStatus().DroppedFills)/float64(b.N), "drops/op")
		})
	}
	for _, hit := range []bool{false, true} {
		b.Run(fmt.Sprintf("response-read/hit=%t", hit), func(b *testing.B) {
			manager, err := cache.NewCacheManager(cache.ManagerConfig{}, nil)
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, manager.Close()) })
			if hit {
				require.NoError(b, manager.SetResponse(b.Context(), "key", []byte("answer")))
				require.Eventually(b, func() bool { _, found, err := manager.GetResponse(b.Context(), "key"); return err == nil && found }, time.Second, time.Millisecond)
			}
			b.ReportAllocs()
			for b.Loop() {
				_, found, err := manager.GetResponse(b.Context(), "key")
				if err != nil || found != hit {
					b.Fatal("unexpected cache result")
				}
			}
		})
	}
}
