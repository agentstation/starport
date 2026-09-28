package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionBudgetPreservesRequestIdentity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			var calls atomic.Int32
			fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true,
				&limits.Limits{Tokens: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}},
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if calls.Add(1) == 1 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":{"message":"retry fixture","type":"server_error"}}`)
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"id\":\"identity\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}\n\ndata: [DONE]\n\n")
					} else {
						budgetReply(w)
					}
				}))
			const requestID = "budget-request-identity"
			body := `{"model":"openai/gpt-4o-mini","models":["openai/gpt-4o-mini","openai/gpt-4o"],"stream":` + strconv.FormatBool(stream) + `,"max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`
			for range 2 {
				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/api/v1/chat/completions", strings.NewReader(body))
				require.NoError(t, err)
				request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Request-ID", requestID)
				response, err := fixture.client.Do(request)
				require.NoError(t, err)
				result, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusOK, response.StatusCode, string(result))
			}
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
			require.NoError(t, err)
			require.Len(t, keys, 3, "one failed attempt and two successful calls need separate reservations")
			require.EqualValues(t, 3, calls.Load())
			ids := map[string]bool{}
			states := map[reservation.State]int{}
			var uncertain reservation.Record
			for _, key := range keys {
				data, err := fixture.application.store.Get(t.Context(), key)
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(data, &record))
				require.Equal(t, requestID, record.Attempt.RequestID)
				require.NotEqual(t, requestID, record.Attempt.ID)
				require.False(t, ids[record.Attempt.ID], "a repeated request ID must not reuse a dispatch permit")
				ids[record.Attempt.ID] = true
				states[record.State]++
				if record.State == reservation.Uncertain {
					uncertain = record
				}
			}
			require.Equal(t, map[reservation.State]int{reservation.Uncertain: 1, reservation.Settled: 2}, states)
			for _, binding := range uncertain.Bindings {
				window, err := fixture.application.budget.ledger.Window(t.Context(), binding.Rule.Meter, uncertain.AdmittedAt)
				require.NoError(t, err)
				require.Equal(t, binding.Amount, window.Reserved)
				require.EqualValues(t, 22, window.Consumed, "both measured retries charge the same token meter")
			}
		})
	}
}
