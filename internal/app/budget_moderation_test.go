package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionModerationBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		budgets *limits.Limits
		status  int
		calls   int64
	}{
		{"explicit free request", &limits.Limits{Spend: &limits.Budget{Limit: 1, Interval: limits.IntervalDay}}, http.StatusOK, 1},
		{"unknown tokens", &limits.Limits{Tokens: &limits.Budget{Limit: 1000000, Interval: limits.IntervalDay}}, http.StatusServiceUnavailable, 0},
		{"confirmed absent budget", nil, http.StatusOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPerformanceFixtureForOperation(t, 0, nil, true, tc.budgets, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model string   `json:"model"`
					Input []string `json:"input"`
				}
				require.NoError(t, json.UnmarshalRead(r.Body, &request))
				require.Equal(t, "omni-moderation-latest", request.Model)
				require.Equal(t, []string{"hello"}, request.Input)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"mod-1","model":"omni-moderation-latest","results":[{"flagged":false,"categories":{"violence":false},"category_scores":{"violence":0.01}}]}`)
			}), []string{"/v1/moderations"})
			response, body := sendModerationRequest(t, fixture)
			require.Equal(t, tc.status, response.StatusCode, string(body))
			require.Equal(t, tc.calls, fixture.calls.Load())
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			if tc.budgets == nil || tc.calls == 0 {
				require.Empty(t, keys)
				return
			}
			require.Len(t, keys, 1)
			data, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			var record reservation.Record
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(t, reservation.Settled, record.State)
			require.NotNil(t, record.NanoUSD)
			require.Zero(t, *record.NanoUSD)
			require.Equal(t, "moderation-request", record.Attempt.RequestID)
		})
	}
}

func sendModerationRequest(t *testing.T, fixture *performanceFixture) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/moderations", strings.NewReader(`{"model":"openai/omni-moderation-latest","input":"hello"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "moderation-request")
	response, err := fixture.client.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response, body
}

func TestProductionGuardrailModerationAdmission(t *testing.T) {
	for _, tc := range []struct {
		name              string
		tokens, forbidden bool
		status            int
		calls             int64
	}{
		{name: "spend and child identity", status: http.StatusOK, calls: 3},
		{name: "token bound unknown", tokens: true, status: http.StatusServiceUnavailable},
		{name: "moderation model forbidden", forbidden: true, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budgets := &limits.Limits{Spend: &limits.Budget{Limit: 100_000_000, Interval: limits.IntervalDay}}
			if tc.tokens {
				budgets.Tokens = &limits.Budget{Limit: 1000000, Interval: limits.IntervalDay}
			}
			fixture := newPerformanceFixtureForProviders(t, 0, nil, true, budgets, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/moderations" {
					_, _ = io.WriteString(w, `{"id":"mod-1","model":"omni-moderation-latest","results":[{"flagged":false,"categories":{"violence":false},"category_scores":{"violence":0.01}}]}`)
				} else {
					_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}}`)
				}
			}), []performanceProvider{{catalogs.ProviderIDOpenAI, "OPENAI_API_KEY", "Authorization", "Bearer sk-test-key", []string{"/v1/moderations", "/v1/chat/completions"}}}, func(key *apikey.APIKey) {
				if tc.forbidden {
					key.AllowedModels = []string{"openai/gpt-4o-mini"}
				}
			}, func(cfg *config.Config) {
				cfg.Guardrails = config.GuardrailsConfig{Checks: "moderation", ModerationModel: "openai/omni-moderation-latest"}
			})
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Request-ID", "guardrail-parent")
			response, err := fixture.client.Do(request)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, tc.status, response.StatusCode, string(body))
			require.Equal(t, tc.calls, fixture.calls.Load())
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			require.Len(t, keys, int(tc.calls))
			var parent reservation.Record
			var children []reservation.Record
			for _, key := range keys {
				data, err := fixture.application.store.Get(t.Context(), key)
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(data, &record))
				require.Equal(t, reservation.Settled, record.State)
				if record.Attempt.RequestID == "guardrail-parent" {
					parent = record
				} else {
					children = append(children, record)
				}
			}
			if tc.calls == 0 {
				return
			}
			require.Len(t, children, 2)
			require.NotEqual(t, children[0].Attempt.ID, children[1].Attempt.ID)
			for _, child := range children {
				require.Equal(t, "guardrail-parent-guardrail", child.Attempt.RequestID)
				require.Equal(t, parent.Attempt.Rules, child.Attempt.Rules)
				require.Equal(t, parent.Attempt.AccountID, child.Attempt.AccountID)
				require.Equal(t, parent.Attempt.KeyID, child.Attempt.KeyID)
				require.Equal(t, parent.Attempt.TeamID, child.Attempt.TeamID)
				require.Zero(t, *child.NanoUSD)
			}
		})
	}
}

func TestProductionModerationFailureRetainsReservation(t *testing.T) {
	fixture := newPerformanceFixtureForOperation(t, 0, nil, true, &limits.Limits{Spend: &limits.Budget{Limit: 1, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid fixture input","type":"invalid_request_error"}}`)
	}), []string{"/v1/moderations"})
	response, body := sendModerationRequest(t, fixture)
	require.NotEqual(t, http.StatusOK, response.StatusCode, string(body))
	require.EqualValues(t, 1, fixture.calls.Load())
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	raw, err := fixture.application.store.Get(t.Context(), keys[0])
	require.NoError(t, err)
	var record reservation.Record
	require.NoError(t, json.Unmarshal(raw, &record))
	require.Equal(t, reservation.Uncertain, record.State)
	require.Zero(t, *record.NanoUSD)
}
