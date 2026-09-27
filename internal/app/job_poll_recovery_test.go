package app

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/stretchr/testify/require"
)

// This fixture exercises the catalog-declared asynchronous transport. It does not qualify live provider support.
func TestProductionVideoPollingPauseAndReconciliation(t *testing.T) {
	for _, prefix := range []string{"/v1", "/api/v1"} {
		t.Run(prefix, func(t *testing.T) {
			var polls atomic.Int64
			fixture := newPerformanceFixtureForProviders(t, 0, nil, true, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodPost:
					_, _ = io.WriteString(w, `{"id":"provider-job","status":"queued"}`)
				case http.MethodGet:
					switch polls.Add(1) {
					case 1:
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":"temporarily unavailable"}`)
					case 2:
						_, _ = io.WriteString(w, `{"id":"provider-job","status":"in_progress"}`)
					default:
						_, _ = io.WriteString(w, `{"id":"provider-job","status":"failed","error":"provider stopped"}`)
					}
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
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
			records, err := jobs.OpenRepository(fixture.application.store)
			require.NoError(t, err)
			original, err := records.Get(t.Context(), "default", answer.ID)
			require.NoError(t, err)
			// Seed an old persisted fixture. Production replacement forbids changing job identity and creation time.
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), jobs.StoragePrefix, 10)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			previous, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			oldTime, err := json.Marshal(original.CreatedAt)
			require.NoError(t, err)
			newTime, err := json.Marshal(original.CreatedAt.Add(-2 * jobs.DefaultLifetime))
			require.NoError(t, err)
			oldField := append([]byte(`"created_at":`), oldTime...)
			require.Equal(t, 1, bytes.Count(previous, oldField))
			aged := bytes.Replace(previous, oldField, append([]byte(`"created_at":`), newTime...), 1)
			require.NoError(t, fixture.application.store.CompareAndSwap(t.Context(), keys[0], previous, aged))
			meter, err := jobslots.Open(fixture.application.store)
			require.NoError(t, err)
			assertSlot := func(expected int64) {
				total, err := meter.Total(t.Context(), "default")
				require.NoError(t, err)
				require.Equal(t, expected, total)
			}
			path := "/videos/" + answer.ID
			for _, readPath := range []string{path, "/videos"} {
				status, data = request(http.MethodGet, readPath, "")
				require.Equal(t, http.StatusOK, status, string(data))
				require.Contains(t, string(data), `"polling_status":"paused"`)
				require.NotContains(t, string(data), "provider-job")
			}
			require.Equal(t, int64(1), fixture.calls.Load())
			assertSlot(1)
			status, data = request(http.MethodPost, path+"/reconcile", "")
			require.GreaterOrEqual(t, status, 400, string(data))
			assertSlot(1)
			for _, state := range []string{"in_progress", "failed"} {
				status, data = request(http.MethodPost, path+"/reconcile", "")
				require.Equal(t, http.StatusOK, status, string(data))
				require.Contains(t, string(data), `"status":"`+state+`"`)
				require.NotContains(t, string(data), "provider-job")
				if state == "in_progress" {
					require.Contains(t, string(data), `"polling_status":"paused"`)
					assertSlot(1)
					status, data = request(http.MethodGet, path, "")
					require.Equal(t, http.StatusOK, status, string(data))
					require.Equal(t, int64(2), polls.Load(), "a manual check must not restart automatic polling")
				} else {
					require.NotContains(t, string(data), "polling_status")
					assertSlot(0)
				}
			}
			require.Equal(t, int64(4), fixture.calls.Load(), "one generation and three status requests")
			status, data = request(http.MethodPost, path+"/reconcile", "")
			require.Equal(t, http.StatusOK, status, string(data))
			require.Equal(t, int64(4), fixture.calls.Load(), "a terminal record must not poll again")
		})
	}
}
