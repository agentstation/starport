package app

import (
	"encoding/json"
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

// The control reproduces the removed map, encode, and typed-decode sequence.
func BenchmarkModelCacheDecode(b *testing.B) {
	for _, size := range []int{1024, 256 << 10} {
		for _, roundTrip := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/map-roundtrip=%t", size, roundTrip), func(b *testing.B) {
				manager, err := cache.NewCacheManager(cache.ManagerConfig{}, nil)
				require.NoError(b, err)
				b.Cleanup(func() { require.NoError(b, manager.Close()) })
				type model struct {
					Description string `json:"description"`
				}
				require.NoError(b, manager.SetModel(b.Context(), "model", model{strings.Repeat("x", size)}))
				require.Eventually(b, func() bool {
					var decoded model
					found, err := manager.GetModel(b.Context(), "model", &decoded)
					return err == nil && found
				}, time.Second, time.Millisecond)
				b.ReportAllocs()
				for b.Loop() {
					var decoded model
					if roundTrip {
						var intermediate map[string]any
						found, err := manager.GetModel(b.Context(), "model", &intermediate)
						if err != nil || !found {
							b.Fatal("missing model")
						}
						data, err := json.Marshal(intermediate)
						if err != nil {
							b.Fatal(err)
						}
						if err := json.Unmarshal(data, &decoded); err != nil {
							b.Fatal(err)
						}
					} else {
						found, err := manager.GetModel(b.Context(), "model", &decoded)
						if err != nil || !found {
							b.Fatal("missing model")
						}
					}
					if len(decoded.Description) != size {
						b.Fatal("invalid model")
					}
				}
			})
		}
	}
}
