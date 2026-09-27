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

func TestProductionRerankBudget(t *testing.T) {
	for _, tc := range []struct {
		name, usage  string
		state        reservation.State
		cost, tokens int64
	}{
		{"measured", `,"usage":{"total_tokens":38}`, reservation.Settled, 1900, 38},
		{"zero", `,"usage":{"total_tokens":0}`, reservation.Settled, 0, 0},
		{"absent", ``, reservation.Uncertain, 3200000, 64000},
		{"null", `,"usage":{"total_tokens":null}`, reservation.Uncertain, 3200000, 64000},
		{"negative", `,"usage":{"total_tokens":-1}`, reservation.Uncertain, 3200000, 64000},
		{"fraction", `,"usage":{"total_tokens":1.5}`, reservation.Uncertain, 3200000, 64000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRerankFixture(t, &limits.Limits{Spend: &limits.Budget{Limit: 10_000_000, Interval: limits.IntervalDay}, Tokens: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Model     string   `json:"model"`
					Query     string   `json:"query"`
					Documents []string `json:"documents"`
					TopK      int      `json:"top_k"`
				}
				require.NoError(t, json.UnmarshalRead(r.Body, &req))
				require.Equal(t, "rerank-2.5", req.Model)
				require.Len(t, req.Documents, 2)
				require.Equal(t, 1, req.TopK)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"index":0,"relevance_score":0.9}]`+tc.usage+`}`)
			}))
			response, body := sendRerankRequest(t, fixture)
			require.Equal(t, http.StatusOK, response.StatusCode, string(body))
			require.EqualValues(t, 1, fixture.calls.Load())
			var envelope map[string]any
			require.NoError(t, json.Unmarshal(body, &envelope))
			wireUsage := envelope["usage"].(map[string]any)
			_, known := wireUsage["total_tokens"]
			require.Equal(t, tc.state == reservation.Settled, known)
			require.NotContains(t, wireUsage, "search_units")
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			data, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			var record reservation.Record
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(t, tc.state, record.State)
			require.Equal(t, tc.cost, *record.NanoUSD)
			if record.Evidence != nil {
				require.Equal(t, tc.tokens, record.Evidence.Tokens)
			} else {
				require.Equal(t, tc.tokens, record.Attempt.TokenBound)
			}
			require.Equal(t, "rerank-parent", record.Attempt.RequestID)
		})
	}
}

func newRerankFixture(t *testing.T, budgets *limits.Limits, upstream http.Handler) *performanceFixture {
	t.Helper()
	return newPerformanceFixtureForProviders(t, 0, nil, true, budgets, upstream, []performanceProvider{{catalogs.ProviderID("voyage"), "VOYAGE_API_KEY", "Authorization", "Bearer sk-test-key", []string{"/v1/rerank"}}}, nil)
}

func sendRerankRequest(t *testing.T, fixture *performanceFixture) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/rerank", strings.NewReader(`{"model":"voyage/rerank-2.5","query":"question","documents":["first","second"],"top_n":1}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "rerank-parent")
	response, err := fixture.client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response, body
}

func TestProductionRerankRefusesInsufficientCapacity(t *testing.T) {
	for _, budgets := range []*limits.Limits{
		{Spend: &limits.Budget{Limit: 3199999, Interval: limits.IntervalDay}},
		{Tokens: &limits.Budget{Limit: 63999, Interval: limits.IntervalDay}},
	} {
		fixture := newRerankFixture(t, budgets, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("over-budget rerank reached provider")
			w.WriteHeader(http.StatusInternalServerError)
		}))
		response, body := sendRerankRequest(t, fixture)
		require.Equal(t, http.StatusPaymentRequired, response.StatusCode, string(body))
		require.Zero(t, fixture.calls.Load())
		keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
		require.NoError(t, err)
		require.Empty(t, keys)
	}
}

func TestProductionRerankInvalidResultRetainsCharge(t *testing.T) {
	fixture := newRerankFixture(t, &limits.Limits{Spend: &limits.Budget{Limit: 10000000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"index":99,"relevance_score":0.9}],"usage":{"total_tokens":38}}`)
	}))
	response, body := sendRerankRequest(t, fixture)
	require.NotEqual(t, http.StatusOK, response.StatusCode, string(body))
	require.EqualValues(t, 1, fixture.calls.Load())
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	raw, err := fixture.application.store.Get(t.Context(), keys[0])
	require.NoError(t, err)
	var record reservation.Record
	require.NoError(t, json.Unmarshal(raw, &record))
	require.Equal(t, reservation.Settled, record.State)
	require.EqualValues(t, 1900, *record.NanoUSD)
}
