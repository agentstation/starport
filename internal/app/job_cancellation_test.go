package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/stretchr/testify/require"
)

// This fixture exercises the catalog-declared asynchronous transport. It does not qualify live provider support.
func TestProductionVideoCancellationRetainsUnconfirmedWork(t *testing.T) {
	for _, prefix := range []string{"/v1", "/api/v1"} {
		for _, tc := range []struct {
			name, body string
			state      jobs.JobState
			valid      bool
		}{
			{"queued", `{"id":"provider-job","status":"queued"}`, jobs.JobStateQueued, true},
			{"running", `{"id":"provider-job","status":"in_progress"}`, jobs.JobStateRunning, true},
			{"deleted", `{"id":"provider-job","deleted":true}`, jobs.JobStateQueued, false},
			{"different job", `{"id":"wrong-job","status":"cancelled"}`, jobs.JobStateQueued, false},
		} {
			t.Run(prefix+"/"+tc.name, func(t *testing.T) {
				fixture := newPerformanceFixtureForProviders(t, 0, nil, true, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.Method {
					case http.MethodPost:
						_, _ = io.WriteString(w, `{"id":"provider-job","status":"queued"}`)
					case http.MethodDelete:
						_, _ = io.WriteString(w, tc.body)
					case http.MethodGet:
						_, _ = io.WriteString(w, `{"id":"provider-job","status":"failed","error":"provider stopped"}`)
					}
				}), []performanceProvider{{catalogs.ProviderID("deepinfra"), "DEEPINFRA_TOKEN", "Authorization", "Bearer sk-test-key", []string{"/openai/videos", "/openai/videos/provider-job"}}}, nil)
				request := func(method, path, body string) (int, []byte) {
					req, err := http.NewRequestWithContext(t.Context(), method, fixture.gateway.URL+prefix+path, strings.NewReader(body))
					require.NoError(t, err)
					req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
					req.Header.Set("Content-Type", "application/json")
					response, err := fixture.client.Do(req)
					require.NoError(t, err)
					data, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					select {
					case <-fixture.handlers:
					default:
					}
					return response.StatusCode, data
				}
				status, data := request(http.MethodPost, "/videos", `{"model":"deepinfra/Wan-AI/Wan2.6-T2V","prompt":"landscape"}`)
				require.Equal(t, http.StatusOK, status, string(data))
				var answer struct {
					ID string `json:"id"`
				}
				require.NoError(t, json.Unmarshal(data, &answer))
				status, data = request(http.MethodPost, "/videos/"+answer.ID+"/cancel", "")
				if tc.valid {
					require.Equal(t, http.StatusOK, status, string(data))
				} else {
					require.GreaterOrEqual(t, status, 400, string(data))
				}
				require.NotContains(t, string(data), "provider-job")
				require.NotContains(t, string(data), "wrong-job")
				records, err := jobs.OpenRepository(fixture.application.store)
				require.NoError(t, err)
				stored, err := records.Get(t.Context(), "default", answer.ID)
				require.NoError(t, err)
				require.Equal(t, tc.state, stored.State)
				require.False(t, stored.SlotReleased)
				meter, err := jobslots.Open(fixture.application.store)
				require.NoError(t, err)
				total, err := meter.Total(t.Context(), "default")
				require.NoError(t, err)
				require.Equal(t, int64(1), total)
				require.Equal(t, int64(2), fixture.calls.Load(), "cancellation cannot repeat the provider submission")
				status, data = request(http.MethodGet, "/videos/"+answer.ID, "")
				require.Equal(t, http.StatusOK, status, string(data))
				stored, err = records.Get(t.Context(), "default", answer.ID)
				require.NoError(t, err)
				require.Equal(t, jobs.JobStateFailed, stored.State)
				require.True(t, stored.SlotReleased)
				require.Equal(t, int64(3), fixture.calls.Load())
				total, err = meter.Total(t.Context(), "default")
				require.NoError(t, err)
				require.Zero(t, total)
			})
		}
	}
}
