package connectors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/stretchr/testify/require"
)

func TestJobCancellationRequiresProviderOutcome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		state  jobs.JobState
	}{
		{name: "queued", body: `{"id":"job-1","status":"queued"}`, state: jobs.JobStateQueued},
		{name: "running", body: `{"id":"job-1","status":"in_progress"}`, state: jobs.JobStateRunning},
		{name: "completed", body: `{"id":"job-1","status":"completed"}`, state: jobs.JobStateCompleted},
		{name: "cancelled", body: `{"id":"job-1","status":"cancelled"}`, state: jobs.JobStateCancelled},
		{name: "failed", body: `{"id":"job-1","status":"failed"}`, state: jobs.JobStateFailed},
		{name: "empty acknowledgement", status: http.StatusNoContent},
		{name: "deletion acknowledgement", body: `{"id":"job-1","deleted":true}`},
		{name: "missing identifier", body: `{"status":"cancelled"}`},
		{name: "different identifier", body: `{"id":"job-2","status":"cancelled"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/videos/job-1", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			connector, err := NewOpenAIConnector(mediaTestConfig(server.URL))
			require.NoError(t, err)
			t.Cleanup(func() { _ = connector.Close() })
			reference := approveConnectorFixture(t, &ProviderJobRef{MediaTarget: MediaTarget{
				Model: "video-model", Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/videos"},
				Credential: testAPIMaterial("test-key"),
			}, ProviderJobID: "job-1"})
			answer, err := connector.CancelJob(t.Context(), reference)
			if tc.state == "" {
				require.Error(t, err)
				require.Nil(t, answer)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.state, answer.State)
			require.Equal(t, reference.ProviderJobID, answer.ID)
		})
	}
}

func TestJobPollRequiresMatchingIdentity(t *testing.T) {
	for _, body := range []string{`{"status":"completed"}`, `{"id":"other","status":"completed"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			connector, err := NewOpenAIConnector(mediaTestConfig(server.URL))
			require.NoError(t, err)
			t.Cleanup(func() { _ = connector.Close() })
			reference := approveConnectorFixture(t, &ProviderJobRef{MediaTarget: MediaTarget{
				Model: "video-model", Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/videos"},
				Credential: testAPIMaterial("test-key"),
			}, ProviderJobID: "job-1"})
			answer, err := connector.PollJob(t.Context(), reference)
			require.ErrorIs(t, err, ErrInvalidMediaRequest)
			require.Nil(t, answer)
		})
	}
}
