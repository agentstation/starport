package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionEmbeddingBudget(t *testing.T) {
	for _, test := range []struct {
		name, usage string
		state       reservation.State
		cost        int64
	}{
		{"measured", `,"usage":{"prompt_tokens":8,"total_tokens":8}`, reservation.Settled, 160},
		{"explicit zero", `,"usage":{"prompt_tokens":0,"total_tokens":0}`, reservation.Settled, 0},
		{"missing usage", ``, reservation.Uncertain, 163840},
		{"missing prompt", `,"usage":{"total_tokens":8}`, reservation.Uncertain, 163840},
		{"null prompt", `,"usage":{"prompt_tokens":null,"total_tokens":8}`, reservation.Uncertain, 163840},
		{"inconsistent", `,"usage":{"prompt_tokens":8,"total_tokens":9}`, reservation.Uncertain, 163840},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPerformanceFixtureForOperation(t, 0, nil, true, &limits.Limits{Spend: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model string `json:"model"`
					Input string `json:"input"`
				}
				require.NoError(t, json.UnmarshalRead(r.Body, &request))
				require.Equal(t, "text-embedding-3-small", request.Model)
				require.Equal(t, "hello", request.Input)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"list","model":"text-embedding-3-small","data":[{"index":0,"embedding":[0.1,0.2]}]`+test.usage+`}`)
			}), []string{"/v1/embeddings"})
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/embeddings", strings.NewReader(`{"model":"openai/text-embedding-3-small","input":"hello"}`))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-ID", "embedding-request")
			response, err := fixture.client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusOK, response.StatusCode, string(body))
			require.EqualValues(t, 1, fixture.calls.Load())
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			data, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			var record reservation.Record
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(t, test.state, record.State)
			require.Equal(t, test.cost, *record.NanoUSD)
			require.Equal(t, "embedding-request", record.Attempt.RequestID)
		})
	}
}

func TestProductionSemanticEmbeddingSharesBudget(t *testing.T) {
	fixture := newPerformanceFixtureForOperation(t, 0, nil, true, &limits.Limits{Spend: &limits.Budget{Limit: 100_000_000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/embeddings" {
			_, _ = io.WriteString(w, `{"object":"list","model":"text-embedding-3-small","data":[{"index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":8,"total_tokens":8}}`)
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}}`)
		}
	}), []string{"/v1/embeddings", "/v1/chat/completions"}, func(cfg *config.Config) {
		cfg.Cache.Enabled = true
		cfg.Cache.ChatEnabled = true
		cfg.SemanticCache = config.SemanticCacheConfig{Enabled: true, Model: "openai/text-embedding-3-small"}
	})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "semantic-parent")
	req.Header.Set("X-Semantic-Cache", "true")
	response, err := fixture.client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	records := map[string]reservation.Record{}
	for _, key := range keys {
		data, err := fixture.application.store.Get(t.Context(), key)
		require.NoError(t, err)
		var record reservation.Record
		require.NoError(t, json.Unmarshal(data, &record))
		records[record.Attempt.RequestID] = record
	}
	child, ok := records["semantic-parent-semantic-cache"]
	require.True(t, ok)
	parent, ok := records["semantic-parent"]
	require.True(t, ok)
	require.Equal(t, reservation.Settled, child.State)
	require.EqualValues(t, 160, *child.NanoUSD)
	require.NotEqual(t, parent.Attempt.ID, child.Attempt.ID)
	require.Equal(t, parent.Attempt.Rules, child.Attempt.Rules)
	require.Equal(t, parent.Attempt.AccountID, child.Attempt.AccountID)
	require.Equal(t, parent.Attempt.KeyID, child.Attempt.KeyID)
	require.Equal(t, reservation.Settled, parent.State)
	require.EqualValues(t, 3000, *parent.NanoUSD)
	require.EqualValues(t, 2, fixture.calls.Load())
}

func TestProductionEmbeddingRefusalBeforeDispatch(t *testing.T) {
	for _, test := range []struct {
		name, model string
		limit       int64
		status      int
	}{
		{"insufficient capacity", "text-embedding-3-small", 100, http.StatusPaymentRequired},
		{"unknown billing", "text-embedding-3-large", 1_000_000, http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPerformanceFixtureForOperation(t, 0, nil, true, &limits.Limits{Spend: &limits.Budget{Limit: test.limit, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("refused embedding reached provider")
				w.WriteHeader(http.StatusInternalServerError)
			}), []string{"/v1/embeddings"})
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/embeddings", strings.NewReader(`{"model":"openai/`+test.model+`","input":"hello"}`))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			request.Header.Set("Content-Type", "application/json")
			response, err := fixture.client.Do(request)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, test.status, response.StatusCode, string(body))
			require.Zero(t, fixture.calls.Load())
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			require.Empty(t, keys)
		})
	}
}
