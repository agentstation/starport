package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionNativeVideoBudget(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		limit          int64
		state          reservation.State
		jobState       jobs.JobState
		status         int
	}{
		{"measured", `{"request_id":"private-native","inference_status":{"output_length":5,"cost":0.99},"video_url":"data:video/mp4;base64,dmlkZW8="}`, 375000000, reservation.Settled, jobs.JobStateCompleted, 200},
		{"missing usage", `{"request_id":"private-native","video_url":"data:video/mp4;base64,dmlkZW8="}`, 375000000, reservation.Uncertain, jobs.JobStateCompleted, 200},
		{"failed with usage", `{"request_id":"private-native","inference_status":{"status":"failed","output_length":5}}`, 375000000, reservation.Settled, jobs.JobStateFailed, 200},
		{"insufficient", `{}`, 374999999, "", "", 402},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			fixture := newPerformanceFixtureForProviders(t, 0, nil, true, &limits.Limits{Spend: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Prompt      string `json:"prompt"`
					Seconds     int64  `json:"seconds"`
					Resolution  string `json:"resolution"`
					Orientation string `json:"orientation"`
				}
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Error(err)
					return
				}
				require.Equal(t, int64(5), body.Seconds)
				require.Equal(t, "720p", body.Resolution)
				require.Equal(t, "landscape", body.Orientation)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}), []performanceProvider{{catalogs.ProviderIDDeepInfra, "DEEPINFRA_TOKEN", "Authorization", "Bearer sk-test-key", []string{"/inference/Wan-AI/Wan2.2-T2V-A14B"}}}, nil)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/videos", strings.NewReader(`{"model":"deepinfra/Wan-AI/Wan2.2-T2V-A14B","prompt":"landscape"}`))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			req.Header.Set("Content-Type", "application/json")
			response, err := fixture.client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, tc.status, response.StatusCode, string(body))
			// The POST must finish while native inference is still waiting for a result.
			if tc.status != 200 {
				require.Zero(t, fixture.calls.Load())
				return
			}
			var object struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.Unmarshal(body, &object))
			require.NotEmpty(t, object.ID)
			require.NotContains(t, string(body), "private-native")
			close(release)
			var attempt reservation.Record
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("attempt state=%s reservation=%v evidence=%+v provider calls=%d", attempt.State, attempt.NanoUSD, attempt.JobID, fixture.calls.Load())
				}
			})
			require.Eventually(t, func() bool {
				keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
				if err != nil || len(keys) != 1 {
					return false
				}
				data, err := fixture.application.store.Get(t.Context(), keys[0])
				if err != nil {
					return false
				}
				return json.Unmarshal(data, &attempt) == nil && attempt.State == tc.state
			}, 10*time.Second, 10*time.Millisecond)
			require.NoError(t, fixture.application.jobs.Close(t.Context()))
			_, err = fixture.application.jobs.Sweep(t.Context())
			if tc.state == reservation.Uncertain {
				require.ErrorIs(t, err, jobs.ErrSettlementPending)
			} else {
				require.NoError(t, err)
			}
			records, err := jobs.OpenRepository(fixture.application.store)
			require.NoError(t, err)
			page, err := records.Scan(t.Context(), 10)
			require.NoError(t, err)
			require.Len(t, page, 1)
			job := page[0]
			require.Equal(t, object.ID, job.ID)
			require.Equal(t, tc.jobState, job.State)
			require.False(t, job.HasProviderJob())
			require.Equal(t, tc.state == reservation.Settled, job.Accounted())
			require.NotNil(t, attempt.NanoUSD)
			require.EqualValues(t, 375000000, *attempt.NanoUSD, "estimated provider cost must not replace pinned duration price")
			require.EqualValues(t, 1, fixture.calls.Load())
			if tc.jobState == jobs.JobStateCompleted {
				_, reader, err := fixture.application.jobs.Open(t.Context(), job.Account, job.ID)
				require.NoError(t, err)
				asset, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
				require.Equal(t, "video", string(asset))
			}
		})
	}
}
