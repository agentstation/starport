package cache

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingCacheUsageProvenance(t *testing.T) {
	for _, test := range []struct {
		name    string
		unknown bool
		legacy  bool
	}{
		{"measured", false, false},
		{"unknown", true, false},
		{"legacy counts without provenance", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			clock := fixedClock{now: time.Unix(100, 0).UTC()}
			repository, err := Open(store, clock)
			require.NoError(t, err)
			response := inference.EmbeddingResponse{Model: "model", Data: []inference.Embedding{{Vector: []float32{0.1, 0.2}}}, Usage: inference.Usage{InputTokens: 4, TotalTokens: 4, TokensUnknown: test.unknown}}
			const key = "embedding"
			if test.legacy {
				response.Usage.TokensUnknown = false
				data, err := json.Marshal(record{SchemaVersion: RecordSchemaVersion, Kind: "embedding", SemanticKey: key, CachedAt: clock.now, Embedding: &response})
				require.NoError(t, err)
				store.values[key] = data
			} else {
				require.NoError(t, repository.PutEmbedding(t.Context(), key, response))
			}
			got, at, found, err := repository.GetEmbedding(t.Context(), key)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, clock.now, at)
			require.Equal(t, response.Data, got.Data)
			require.Equal(t, test.unknown, got.Usage.TokensUnknown)
			require.Equal(t, 4, got.Usage.InputTokens)
		})
	}
}
