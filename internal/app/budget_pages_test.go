package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionPageRecognitionBudget(t *testing.T) {
	for _, tc := range []struct {
		name, usage, pages string
		state              reservation.State
		cost               int64
		success            bool
	}{
		{"measured", `,"usage_info":{"pages_processed":1}`, `[{"index":0,"markdown":"Invoice"}]`, reservation.Settled, 4000000, true},
		{"duplicate index retains charge", `,"usage_info":{"pages_processed":1}`, `[{"index":0},{"index":0}]`, reservation.Settled, 4000000, false},
		{"short document retains charge", `,"usage_info":{"pages_processed":1}`, `[]`, reservation.Settled, 4000000, false},
		{"missing usage", ``, `[{"index":0,"markdown":"Invoice"}]`, reservation.Uncertain, 4000000, true},
		{"null count", `,"usage_info":{"pages_processed":null}`, `[]`, reservation.Uncertain, 4000000, false},
		{"zero count", `,"usage_info":{"pages_processed":0}`, `[]`, reservation.Settled, 0, false},
		{"negative count", `,"usage_info":{"pages_processed":-1}`, `[]`, reservation.Uncertain, 4000000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPageRecognitionFixture(t, &limits.Limits{Spend: &limits.Budget{Limit: 2000000000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/ocr" {
					var body map[string]any
					require.NoError(t, json.UnmarshalRead(r.Body, &body))
					require.Equal(t, "mistral-ocr-4-0", body["model"])
					require.Equal(t, []any{float64(0)}, body["pages"])
					require.NotContains(t, body, "document_annotation_format")
					require.NotContains(t, body, "bbox_annotation_format")
					_, _ = io.WriteString(w, `{"model":"mistral-ocr-4-0","pages":`+tc.pages+tc.usage+`}`)
					return
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}}`)
			}))
			response, body := sendRecognitionRequest(t, fixture)
			if tc.success {
				require.Equal(t, http.StatusOK, response.StatusCode, string(body))
			} else {
				require.NotEqual(t, http.StatusOK, response.StatusCode, string(body))
			}
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			want := 1
			if tc.success {
				want++
			}
			require.Len(t, keys, want, string(body))
			require.EqualValues(t, want, fixture.calls.Load())
			found := false
			for _, key := range keys {
				raw, err := fixture.application.store.Get(t.Context(), key)
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(raw, &record))
				if record.Attempt.OfferingID != "mistral/mistral-ocr-4-0" {
					continue
				}
				found = true
				require.Equal(t, tc.state, record.State)
				require.Equal(t, tc.cost, *record.NanoUSD)
				require.Equal(t, "recognition-parent", record.Attempt.RequestID)
			}
			require.True(t, found)
		})
	}
}

func newPageRecognitionFixture(t *testing.T, budgets *limits.Limits, handler http.Handler) *performanceFixture {
	t.Helper()
	return newPerformanceFixtureForProviders(t, 0, nil, true, budgets, handler, []performanceProvider{
		{catalogs.ProviderIDOpenAI, "OPENAI_API_KEY", "Authorization", "Bearer sk-test-key", []string{"/v1/chat/completions"}},
		{catalogs.ProviderIDMistralAI, "MISTRAL_API_KEY", "Authorization", "Bearer sk-test-key", []string{"/v1/ocr"}},
	}, func(key *apikey.APIKey) {
		key.AllowedModels = []string{"openai/gpt-4o-mini", "mistral/mistral-ocr-4-0"}
	})
}

func TestProductionPageRecognitionRefusesUnknownTokens(t *testing.T) {
	fixture := newPageRecognitionFixture(t, &limits.Limits{Tokens: &limits.Budget{Limit: 2000000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unknown token bound reached provider")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	response, body := sendRecognitionRequest(t, fixture)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode, string(body))
	require.Zero(t, fixture.calls.Load())
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestProductionPageRecognitionRefusesInsufficientSpend(t *testing.T) {
	fixture := newPageRecognitionFixture(t, &limits.Limits{Spend: &limits.Budget{Limit: 3999999, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("over-budget request reached provider")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	response, body := sendRecognitionRequest(t, fixture)
	require.Equal(t, http.StatusPaymentRequired, response.StatusCode, string(body))
	require.Zero(t, fixture.calls.Load())
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
	require.NoError(t, err)
	require.Empty(t, keys)
}
