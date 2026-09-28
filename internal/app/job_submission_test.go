package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/stretchr/testify/require"
)

// This fixture exercises the catalog-declared asynchronous transport. It does not qualify live provider support.
func TestProductionVideoSubmissionPersistsBeforeDispatch(t *testing.T) {
	for _, prefix := range []string{"/v1", "/api/v1"} {
		for _, tc := range []struct {
			name, body     string
			status         int
			pending        bool
			requiredBudget bool
		}{
			{"accepted", `{"id":"provider-job","status":"queued"}`, 200, false, false},
			{"provider unavailable", `{"error":{"message":"unavailable"}}`, 503, true, false},
			{"missing handle", `{"status":"queued"}`, 200, true, false},
			{"invalid response", `{`, 200, true, false},
			{"required budget remains unqualified", "", 503, false, true},
		} {
			t.Run(prefix+"/"+tc.name, func(t *testing.T) {
				var fixture *performanceFixture
				var budget *limits.Limits
				if tc.requiredBudget {
					budget = &limits.Limits{Spend: &limits.Budget{Limit: 1000000000, Interval: limits.IntervalDay}}
				}
				fixture = newPerformanceFixtureForProviders(t, 0, nil, true, budget, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					held, err := fixture.application.jobs.List(r.Context(), "default", 10)
					require.NoError(t, err)
					require.Len(t, held, 1, "job persistence must precede the actual HTTP provider call")
					require.True(t, held[0].SubmissionPending)
					require.False(t, held[0].HasProviderJob())
					require.Equal(t, fixture.generation, held[0].CatalogGeneration)
					meter, err := jobslots.Open(fixture.application.store)
					require.NoError(t, err)
					slot, err := meter.Get(r.Context(), "default", held[0].SlotID)
					require.NoError(t, err, "the HTTP provider call requires durable slot ownership")
					require.Equal(t, held[0].ID, slot.JobID)
					require.Equal(t, "video", slot.Kind)
					require.False(t, slot.Released)
					require.True(t, slot.Attached, "claim attachment and the job record must commit together")
					total, err := meter.Total(r.Context(), "default")
					require.NoError(t, err)
					require.Equal(t, int64(1), total)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				}), []performanceProvider{{catalogs.ProviderID("deepinfra"), "DEEPINFRA_TOKEN", "Authorization", "Bearer sk-test-key", []string{"/openai/videos"}}}, nil)
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+prefix+"/videos", strings.NewReader(`{"model":"deepinfra/Wan-AI/Wan2.6-T2V","prompt":"landscape"}`))
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
				status := 200
				if tc.pending || tc.requiredBudget {
					status = 503
				}
				require.Equal(t, status, response.StatusCode, string(data))
				calls := int64(1)
				if tc.requiredBudget {
					calls = 0
				}
				require.Equal(t, calls, fixture.calls.Load(), "an ambiguous asynchronous submission cannot retry")
				records, err := jobs.OpenRepository(fixture.application.store)
				require.NoError(t, err)
				held, err := records.Scan(t.Context(), 10)
				require.NoError(t, err)
				if tc.requiredBudget {
					require.Empty(t, held)
					return
				}
				require.Len(t, held, 1)
				require.Equal(t, tc.pending, held[0].SubmissionPending)
				require.Equal(t, !tc.pending, held[0].HasProviderJob())
				require.NotContains(t, string(data), "provider-job")
				if tc.pending {
					require.Equal(t, prefix+"/videos/"+held[0].ID, response.Header.Get("Location"))
					require.Contains(t, string(data), held[0].ID)
					for _, path := range []string{prefix + "/videos", response.Header.Get("Location")} {
						followup, err := http.NewRequestWithContext(t.Context(), http.MethodGet, fixture.gateway.URL+path, nil)
						require.NoError(t, err)
						followup.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
						status, err := fixture.client.Do(followup)
						require.NoError(t, err)
						body, err := io.ReadAll(status.Body)
						require.NoError(t, err)
						require.NoError(t, status.Body.Close())
						select {
						case <-fixture.handlers:
						default:
						}
						if path == prefix+"/videos" {
							require.Equal(t, 200, status.StatusCode, string(body))
							require.Contains(t, string(body), `"submission_status":"unconfirmed"`)
						} else {
							require.Equal(t, 503, status.StatusCode, string(body))
						}
						require.NotContains(t, string(body), "provider-job")
					}
					require.Equal(t, int64(1), fixture.calls.Load())
				} else {
					var answer struct {
						ID string `json:"id"`
					}
					require.NoError(t, json.Unmarshal(data, &answer))
					require.Equal(t, held[0].ID, answer.ID)
				}
			})
		}
	}
}
