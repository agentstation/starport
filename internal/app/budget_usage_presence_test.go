package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionSpendMissingTokenCountRetainsCapacity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			counts string
			state  reservation.State
		}{
			{`"completion_tokens":3,"total_tokens":3`, reservation.Uncertain},
			{`"prompt_tokens":null,"completion_tokens":3,"total_tokens":3`, reservation.Uncertain},
			{`"prompt_tokens":8,"total_tokens":8`, reservation.Uncertain},
			{`"prompt_tokens":0,"completion_tokens":0,"total_tokens":0`, reservation.Settled},
		} {
			t.Run(strconv.FormatBool(stream)+"/"+test.counts, func(t *testing.T) {
				fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true,
					&limits.Limits{Spend: &limits.Budget{Limit: 100_000_000, Interval: limits.IntervalDay}},
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, `data: {"id":"usage-presence","choices":[{"index":0,"delta":{"content":"hello"}}],"usage":{`+test.counts+`,"prompt_tokens_details":{"cached_tokens":0}}}`+"\n\ndata: [DONE]\n\n")
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, `{"id":"usage-presence","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{`+test.counts+`,"prompt_tokens_details":{"cached_tokens":0}}}`)
						}
					}))
				body := `{"model":"openai/gpt-4o-mini","stream":` + strconv.FormatBool(stream) + `,"max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`
				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/chat/completions", strings.NewReader(body))
				require.NoError(t, err)
				request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
				request.Header.Set("Content-Type", "application/json")
				response, err := fixture.client.Do(request)
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusOK, response.StatusCode)
				keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
				require.NoError(t, err)
				require.Len(t, keys, 1)
				data, err := fixture.application.store.Get(t.Context(), keys[0])
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(data, &record))
				require.Equal(t, test.state, record.State)
				if test.state == reservation.Uncertain {
					require.Nil(t, record.Evidence)
					require.Greater(t, *record.NanoUSD, int64(1_000_000))
				} else {
					require.NotNil(t, record.Evidence)
					require.Zero(t, *record.NanoUSD)
				}
			})
		}
	}
}
