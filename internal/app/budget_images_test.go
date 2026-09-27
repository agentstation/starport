package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionImagesBudget(t *testing.T) {
	for _, tc := range []struct {
		name, model, size, response string
		n                           int
		limit, cost                 int64
		noBudget, tokens, truncated bool
		upstream, status            int
		calls                       int64
		state                       reservation.State
	}{
		{name: "flat images", model: "FLUX-1.1-pro", size: "1024x1024", n: 2, limit: 80000000, cost: 80000000, status: 200, calls: 1, state: reservation.Settled},
		{name: "scaled pixels", model: "FLUX-1-schnell", size: "1024x1536", n: 2, limit: 1500000, cost: 1500000, status: 200, calls: 1, state: reservation.Settled},
		{name: "omitted defaults", model: "FLUX-1-schnell", limit: 500000, cost: 500000, response: `{"data":[{"b64_json":"aW1hZ2U="}]}`, status: 200, calls: 1, state: reservation.Settled},
		{name: "fewer returned images", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 40000000, response: `{"data":[{"b64_json":"aW1hZ2U="}]}`, status: 200, calls: 1, state: reservation.Settled},
		{name: "empty item retains full bound", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, response: `{"data":[{}]}`, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "no items retains full bound", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, response: `{"data":[]}`, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "truncated response", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, truncated: true, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "trailing response", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, response: `{"data":[{"b64_json":"aW1hZ2U="}]} {}`, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "provider failure", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, upstream: 500, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "insufficient capacity", model: "FLUX-1.1-pro", n: 2, limit: 79999999, status: 402},
		{name: "unknown tokens", model: "FLUX-1.1-pro", n: 2, limit: 100000000, tokens: true, status: 503},
		{name: "no budget", model: "FLUX-1.1-pro", n: 2, noBudget: true, status: 200, calls: 1},
		{name: "unknown dimensions", model: "FLUX-1-schnell", size: "auto", n: 2, limit: 100000000, status: 503},
		{name: "overflow dimensions", model: "FLUX-1-schnell", size: "9223372036854775807x2", n: 2, limit: 100000000, status: 503},
		{name: "unqualified unit", model: "FLUX-1-dev", size: "1024x1024", n: 2, limit: 100000000, status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := &limits.Limits{Spend: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}
			if tc.tokens {
				budget = &limits.Limits{Tokens: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}
			}
			if tc.noBudget {
				budget = nil
			}
			model := "black-forest-labs/" + tc.model
			fixture := newPerformanceFixtureForProviders(t, 0, nil, true, budget, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Model string `json:"model"`
					Size  string `json:"size"`
					N     int    `json:"n"`
				}
				require.NoError(t, json.UnmarshalRead(r.Body, &wire))
				require.Equal(t, model, wire.Model)
				require.Equal(t, tc.size, wire.Size)
				require.Equal(t, tc.n, wire.N)
				w.Header().Set("Content-Type", "application/json")
				if tc.truncated {
					w.Header().Set("Content-Length", "10000")
				}
				upstream := tc.upstream
				if upstream == 0 {
					upstream = 200
				}
				w.WriteHeader(upstream)
				response := tc.response
				if response == "" {
					response = `{"data":[{"b64_json":"aW1hZ2U="},{"b64_json":"aW1hZ2U="}]}`
				}
				_, _ = io.WriteString(w, response)
			}), []performanceProvider{{catalogs.ProviderID("deepinfra"), "DEEPINFRA_TOKEN", "Authorization", "Bearer sk-test-key", []string{"/images/generations"}}}, nil)
			raw, err := json.Marshal(map[string]any{"model": "deepinfra/" + model, "prompt": "landscape", "n": tc.n, "size": tc.size})
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/images/generations", strings.NewReader(string(raw)))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			req.Header.Set("Content-Type", "application/json")
			response, err := fixture.client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			if tc.status >= 500 && tc.calls > 0 {
				require.GreaterOrEqual(t, response.StatusCode, 500, string(body))
			} else {
				require.Equal(t, tc.status, response.StatusCode, string(body))
			}
			require.Equal(t, tc.calls, fixture.calls.Load())
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			if tc.calls == 0 || tc.noBudget {
				require.Empty(t, keys)
				return
			}
			require.Len(t, keys, 1)
			data, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			var record reservation.Record
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(t, tc.state, record.State)
			require.NotNil(t, record.NanoUSD)
			require.Equal(t, tc.cost, *record.NanoUSD)
		})
	}
}
