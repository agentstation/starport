package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionResponsesBudget(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		stream, incomplete   bool
		absent, insufficient bool
	}{
		{name: "measured"},
		{name: "missing usage", incomplete: true},
		{name: "stream measured", stream: true},
		{name: "stream truncated", stream: true, incomplete: true},
		{name: "insufficient capacity", insufficient: true},
		{name: "stream insufficient capacity", stream: true, insufficient: true},
		{name: "confirmed absent", absent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budgets := &limits.Limits{
				Tokens: &limits.Budget{Limit: 200_000, Interval: limits.IntervalDay},
				Spend:  &limits.Budget{Limit: 100_000_000, Interval: limits.IntervalDay},
			}
			if tc.insufficient {
				budgets.Spend.Limit = 1
			}
			if tc.absent {
				budgets = nil
			}
			fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true, budgets,
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "/v1/chat/completions", r.URL.Path)
					var wire struct {
						Model     string `json:"model"`
						MaxTokens int    `json:"max_tokens"`
						Stream    bool   `json:"stream"`
					}
					require.NoError(t, json.UnmarshalRead(r.Body, &wire))
					require.Equal(t, "gpt-4o-mini", wire.Model)
					require.Equal(t, 32, wire.MaxTokens)
					require.Equal(t, tc.stream, wire.Stream)
					usage := `,"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}`
					if tc.stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"id\":\"response-budget\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n")
						if !tc.incomplete {
							_, _ = io.WriteString(w, `data: {"id":"response-budget","choices":[]`+usage+"}\n\ndata: [DONE]\n\n")
						}
						return
					}
					if tc.incomplete {
						usage = ""
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"response-budget","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]`+usage+`}`)
				}))
			payload, err := json.Marshal(map[string]any{"model": "openai/gpt-4o-mini", "input": "Hello", "max_output_tokens": 32, "stream": tc.stream})
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/responses", strings.NewReader(string(payload)))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-ID", "responses-parent")
			response, err := fixture.client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			if tc.insufficient {
				require.Equal(t, http.StatusPaymentRequired, response.StatusCode, string(body))
				require.Zero(t, fixture.calls.Load())
			} else {
				require.Equal(t, http.StatusOK, response.StatusCode, string(body))
				require.EqualValues(t, 1, fixture.calls.Load())
				if tc.stream && !tc.incomplete {
					require.Contains(t, string(body), "event: response.completed")
				}
			}
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			if tc.absent || tc.insufficient {
				require.Empty(t, keys)
				return
			}
			require.Len(t, keys, 1, "the Responses adapter must not create a second charge")
			var record reservation.Record
			require.Eventually(t, func() bool {
				data, err := fixture.application.store.Get(t.Context(), keys[0])
				if err != nil || json.Unmarshal(data, &record) != nil {
					return false
				}
				return record.State == reservation.Settled || record.State == reservation.Uncertain
			}, 3*time.Second, 10*time.Millisecond)
			require.Equal(t, "responses-parent", record.Attempt.RequestID)
			require.Equal(t, "chat-completions", record.Attempt.Operation)
			require.Len(t, record.Bindings, 2)
			if tc.incomplete {
				require.Equal(t, reservation.Uncertain, record.State)
				require.Nil(t, record.Evidence)
				require.Equal(t, http.StatusPaymentRequired, budgetDispatch(t, fixture))
				require.EqualValues(t, 1, fixture.calls.Load())
			} else {
				require.Equal(t, reservation.Settled, record.State)
				require.NotNil(t, record.Evidence)
				require.EqualValues(t, 11, record.Evidence.Tokens)
				require.NotNil(t, record.NanoUSD)
				require.EqualValues(t, 3000, *record.NanoUSD)
			}
		})
	}
}
