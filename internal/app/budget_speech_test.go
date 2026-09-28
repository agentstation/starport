package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionSpeechBudget(t *testing.T) {
	for _, tc := range []struct {
		name, model, input string
		limit, cost        int64
		tokens             bool
		noBudget           bool
		emptyAudio         bool
		truncatedAudio     bool
		upstream, status   int
		calls              int64
		state              reservation.State
	}{
		{name: "measured characters", model: "tts-1", input: "hello", limit: 75000, cost: 75000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "unicode code points", model: "tts-1", input: "é🦊", limit: 30000, cost: 30000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "combining mark and whitespace", model: "tts-1", input: "e\u0301 !", limit: 60000, cost: 60000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "maximum input", model: "tts-1", input: strings.Repeat("🦊", 4096), limit: 61440000, cost: 61440000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "input exceeds bound", model: "tts-1", input: strings.Repeat("a", 4097), limit: 100000000, upstream: 200, status: 503},
		{name: "no required budget", model: "tts-1", input: "hello", noBudget: true, upstream: 200, status: 200, calls: 1},
		{name: "empty audio retains charge", model: "tts-1", input: "hello", limit: 75000, cost: 75000, emptyAudio: true, upstream: 200, status: 500, calls: 1, state: reservation.Settled},
		{name: "truncated audio retains capacity", model: "tts-1", input: "hello", limit: 75000, cost: 75000, truncatedAudio: true, upstream: 200, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "HD price", model: "tts-1-hd", input: "hello", limit: 150000, cost: 150000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "insufficient capacity", model: "tts-1", input: "hello", limit: 74999, upstream: 200, status: 402},
		{name: "unknown tokens", model: "tts-1", input: "hello", limit: 1000000, tokens: true, upstream: 200, status: 503},
		{name: "provider failure", model: "tts-1", input: "hello", limit: 75000, cost: 75000, upstream: 500, status: 500, calls: 1, state: reservation.Uncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := &limits.Limits{Spend: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}
			if tc.tokens {
				budget = &limits.Limits{Tokens: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}
			}
			if tc.noBudget {
				budget = nil
			}
			fixture := newPerformanceFixtureForOperation(t, 0, nil, true, budget, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Model string `json:"model"`
					Input string `json:"input"`
				}
				require.NoError(t, json.UnmarshalRead(r.Body, &wire))
				require.Equal(t, tc.model, wire.Model)
				require.Equal(t, tc.input, wire.Input)
				w.Header().Set("Content-Type", "audio/mpeg")
				if tc.truncatedAudio {
					w.Header().Set("Content-Length", "100")
				}
				w.WriteHeader(tc.upstream)
				if !tc.emptyAudio {
					_, _ = io.WriteString(w, "audio")
				}
			}), []string{"/v1/audio/speech"})
			raw, err := json.Marshal(map[string]any{"model": "openai/" + tc.model, "input": tc.input, "voice": "alloy"})
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/audio/speech", strings.NewReader(string(raw)))
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
