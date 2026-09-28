package app

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

// The fixture qualifies the asynchronous transport, not live provider support.
func TestProductionVideoFollowupBudget(t *testing.T) {
	for _, prefix := range []string{"/v1", "/api/v1"} {
		for _, purpose := range []string{"poll", "cancel", "content"} {
			for _, meter := range []string{"spend", "tokens", "absent"} {
				t.Run(prefix+"/"+purpose+"/"+meter, func(t *testing.T) {
					var budgets *limits.Limits
					switch meter {
					case "spend":
						budgets = &limits.Limits{Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay}}
					case "tokens":
						budgets = &limits.Limits{Tokens: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}}
					}
					fixture := newPerformanceFixtureForProviders(t, 0, nil, true, budgets,
						http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							require.NotEqual(t, http.MethodPost, r.Method, "follow-up calls must never submit generation")
							switch purpose {
							case "content":
								require.Equal(t, http.MethodGet, r.Method)
								require.Equal(t, "/openai/videos/provider-job/content", r.URL.Path)
								w.Header().Set("Content-Type", "video/mp4")
								_, _ = io.WriteString(w, "video fixture")
							case "cancel":
								require.Equal(t, http.MethodDelete, r.Method)
								w.Header().Set("Content-Type", "application/json")
								_, _ = io.WriteString(w, `{"id":"provider-job","status":"queued"}`)
							case "poll":
								require.Equal(t, http.MethodGet, r.Method)
								w.Header().Set("Content-Type", "application/json")
								_, _ = io.WriteString(w, `{"id":"provider-job","status":"queued"}`)
							}
						}), []performanceProvider{{catalogs.ProviderID("deepinfra"), "DEEPINFRA_TOKEN", "Authorization", "Bearer sk-test-key", []string{"/openai/videos/provider-job", "/openai/videos/provider-job/content"}}}, nil)
					// Seed accepted work. This test makes no claim about submission billing.
					job, err := jobs.New("video-followup", "default", "deepinfra", "deepinfra/Wan-AI/Wan2.6-T2V", routing.OperationVideosGenerations, time.Now())
					require.NoError(t, err)
					require.NoError(t, job.AdoptProviderJob("provider-job"))
					job.KeyID = testAPIKey().ID
					if purpose == "content" {
						require.NoError(t, job.Transition(jobs.JobStateCompleted, time.Now()))
					}
					records, err := jobs.OpenRepository(fixture.application.store)
					require.NoError(t, err)
					require.NoError(t, records.Create(t.Context(), job))
					path := prefix + "/videos/" + job.ID
					method := http.MethodGet
					if purpose == "cancel" {
						method = http.MethodPost
						path += "/cancel"
					}
					req, err := http.NewRequestWithContext(t.Context(), method, fixture.gateway.URL+path, nil)
					require.NoError(t, err)
					req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
					response, err := fixture.client.Do(req)
					require.NoError(t, err)
					body, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					if meter == "absent" {
						require.Equal(t, http.StatusOK, response.StatusCode, string(body))
						require.EqualValues(t, 1, fixture.calls.Load())
					} else {
						require.Zero(t, fixture.calls.Load(), "unknown follow-up billing cannot spend required capacity")
						if purpose == "content" {
							// A failed asset fetch must preserve the completed generation.
							require.Equal(t, http.StatusOK, response.StatusCode, string(body))
						} else {
							require.Equal(t, http.StatusServiceUnavailable, response.StatusCode, string(body))
							require.Contains(t, string(body), "verified billing bound")
						}
					}
					require.NotContains(t, string(body), "provider-job")
					retained, err := records.Get(t.Context(), "default", job.ID)
					require.NoError(t, err)
					require.Equal(t, job.State, retained.State)
					require.True(t, job.CreatedAt.Equal(retained.CreatedAt), "persisted creation time must remain unchanged")
					require.True(t, retained.HasProviderJob())
					if purpose == "content" {
						require.Equal(t, meter == "absent", retained.HasAsset())
						if meter == "absent" {
							_, reader, err := fixture.application.jobs.Open(t.Context(), "default", job.ID)
							require.NoError(t, err)
							asset, err := io.ReadAll(reader)
							require.NoError(t, err)
							require.NoError(t, reader.Close())
							require.Equal(t, "video fixture", string(asset))
							require.EqualValues(t, 1, fixture.calls.Load())
						}
					}
					attempts, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
					require.NoError(t, err)
					require.Empty(t, attempts, "neither absent policy nor refused dispatch reserves generation again")
				})
			}
		}
	}
}
