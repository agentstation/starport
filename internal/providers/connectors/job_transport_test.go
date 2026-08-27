package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/jobs"
)

// TestADescriptorCannotDeclareVideoJobsWithoutARunner holds the guard at
// activation. A descriptor states the operation, the catalog publishes it, and
// a caller submits a job that no code can run. The refusal has its own error,
// because a caller that cannot submit at all needs a different answer from one
// whose image call failed inside its request.
func TestADescriptorCannotDeclareVideoJobsWithoutARunner(t *testing.T) {
	registry, err := NewTransportRegistry(TransportDescriptor{
		EndpointType: catalogs.EndpointTypeOpenAI,
		Operations: []catalogs.ProviderOperation{
			catalogs.ProviderOperationChatCompletions,
			catalogs.ProviderOperationVideosGenerations,
		},
		// MockConnector serves chat and embeddings and implements no job
		// interface, which is the exact shape of a transport left behind when a
		// descriptor gains the operation.
		Factory: func(catalogs.ProviderID, ProviderConfig) (Connector, error) {
			return NewMockConnector(ProviderConfig{}), nil
		},
	})
	require.NoError(t, err)

	_, err = registry.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		mediaTestConfig("https://provider.example"),
	)
	require.ErrorIs(t, err, ErrJobsUnsupported)
	require.Contains(t, err.Error(), "JobRunner")
	require.Contains(t, err.Error(), string(catalogs.ProviderOperationVideosGenerations))
}

// TestTheCompiledTransportRunsVideoJobs is the other half of the guard. The
// shipped registry declares the operation, so a refusal here would mean no
// provider could run a video job at all and the test above would pass for the
// wrong reason.
func TestTheCompiledTransportRunsVideoJobs(t *testing.T) {
	registry, err := ProductionTransportRegistry()
	require.NoError(t, err)
	require.True(t, registry.Supports(
		catalogs.EndpointTypeOpenAI,
		catalogs.ProviderOperationVideosGenerations,
	))

	connector, err := registry.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI, catalogs.EndpointTypeAnthropic},
		mediaTestConfig("https://provider.example"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connector.Close() })

	// The probe reads the transport the route selected. Only the OpenAI
	// protocol compiles a job transport, so probing the composed connector
	// would report a capability the Anthropic protocol lacks.
	_, openAIRuns := JobRunnerFor(connector, catalogs.EndpointTypeOpenAI)
	require.True(t, openAIRuns)
	_, anthropicRuns := JobRunnerFor(connector, catalogs.EndpointTypeAnthropic)
	require.False(t, anthropicRuns)

	// Connector gained no job method. A chat-only transport would have to
	// answer three calls it cannot perform.
	names := make([]string, 0)
	connectorType := reflect.TypeOf((*Connector)(nil)).Elem()
	for index := range connectorType.NumMethod() {
		names = append(names, connectorType.Method(index).Name)
	}
	require.Equal(t, []string{"Chat", "ChatStream", "Close", "Embeddings", "Name"}, names)
}

// TestAnUnknownProviderStateWordFailsLoudly is the reason the state map is a
// closed set. A word that fell through to running would poll a finished job
// until its lifetime ran out, and one that fell through to failed would discard
// an asset the caller already paid for. Neither reports anything.
func TestAnUnknownProviderStateWordFailsLoudly(t *testing.T) {
	t.Parallel()

	known := map[string]jobs.JobState{
		"queued":      jobs.JobStateQueued,
		"pending":     jobs.JobStateQueued,
		"in_progress": jobs.JobStateRunning,
		"completed":   jobs.JobStateCompleted,
		"failed":      jobs.JobStateFailed,
		"expired":     jobs.JobStateFailed,
		"cancelled":   jobs.JobStateCancelled,
	}
	// The map and the test list the same words, so a word added to one without
	// the other is a failure rather than a silent widening of the set.
	require.Len(t, ProviderStateWords(), len(known))
	for _, word := range ProviderStateWords() {
		require.Contains(t, known, word)
	}

	for word, want := range known {
		state, reason, err := ProviderJobState(word)
		require.NoErrorf(t, err, "word %q", word)
		require.Equalf(t, want, state, "word %q", word)
		if word == "expired" {
			require.NotEmpty(t, reason, "an expired job states why it produced nothing")
		}
	}

	// Case and surrounding space are provider formatting rather than meaning.
	state, _, err := ProviderJobState("  IN_PROGRESS ")
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateRunning, state)

	for _, unknown := range []string{"", "processing", "succeeded", "running", "done", "queued_2"} {
		state, _, err := ProviderJobState(unknown)
		require.ErrorIsf(t, err, ErrUnknownProviderState, "word %q", unknown)
		require.Emptyf(t, state, "word %q read as a state", unknown)
	}
}

// TestAJobPollReadsTheProviderAnswer walks the three job methods against a
// provider that answers the way the recorded surfaces do. It holds the paths,
// the methods, and the failure reason together, because a poll that addressed
// the collection rather than one job would still decode a valid answer.
func TestAJobPollReadsTheProviderAnswer(t *testing.T) {
	type call struct {
		method string
		path   string
	}
	var calls []call

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, call{method: r.Method, path: r.URL.Path})
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			// A submission starts work rather than completing it, so the
			// provider answers 202 and the job is not finished.
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"video_77","status":"queued"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"id":"video_77","status":"failed","error":{"code":"content_policy","message":"the prompt was refused"}}`))
		}
	}))
	defer server.Close()

	connector, err := NewOpenAIConnector(mediaTestConfig(server.URL + "/v1"))
	require.NoError(t, err)
	target := MediaTarget{
		Model:      "provider/video",
		Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/v1/videos"},
		Credential: testAPIMaterial("test-key"),
	}

	submitted, err := connector.SubmitJob(context.Background(), &JobSubmission{
		MediaTarget: target,
		Prompt:      "a boat leaving a harbour",
	})
	require.NoError(t, err)
	require.Equal(t, "video_77", submitted.ID)
	require.Equal(t, jobs.JobStateQueued, submitted.State)

	reference := &ProviderJobRef{MediaTarget: target, ProviderJobID: submitted.ID}
	polled, err := connector.PollJob(context.Background(), reference)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateFailed, polled.State)
	require.Equal(t, "the prompt was refused", polled.Reason)

	cancelled, err := connector.CancelJob(context.Background(), reference)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCancelled, cancelled.State)

	require.Equal(t, []call{
		{method: http.MethodPost, path: "/v1/videos"},
		{method: http.MethodGet, path: "/v1/videos/video_77"},
		{method: http.MethodDelete, path: "/v1/videos/video_77"},
	}, calls)
}

// TestAFailedJobAlwaysStatesAReason holds the contract between the two
// packages. The job record refuses a failed job that states nothing, so a
// decoder that left the reason empty would make a real provider answer
// unstorable. Two provider families report the error under two shapes.
func TestAFailedJobAlwaysStatesAReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "an object with a message",
			body: `{"id":"v1","status":"failed","error":{"code":"policy","message":"the prompt was refused"}}`,
			want: "the prompt was refused",
		},
		{
			name: "an object with a code alone",
			body: `{"id":"v1","status":"failed","error":{"code":"policy"}}`,
			want: "policy",
		},
		{
			name: "a bare string",
			body: `{"id":"v1","status":"failed","error":"the model is overloaded"}`,
			want: "the model is overloaded",
		},
		{
			name: "no error at all",
			body: `{"id":"v1","status":"failed"}`,
			want: "the provider reported a failure and stated no reason",
		},
		{
			name: "a provider that expired the job",
			body: `{"id":"v1","status":"expired"}`,
			want: "the provider expired the job before Starport collected its asset",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			connector, err := NewOpenAIConnector(mediaTestConfig(server.URL + "/v1"))
			require.NoError(t, err)
			polled, err := connector.PollJob(context.Background(), &ProviderJobRef{
				MediaTarget: MediaTarget{
					Model:      "provider/video",
					Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/v1/videos"},
					Credential: testAPIMaterial("test-key"),
				},
				ProviderJobID: "v1",
			})
			require.NoError(t, err)
			require.Equal(t, jobs.JobStateFailed, polled.State)
			require.Equal(t, test.want, polled.Reason)

			// The record is the reader of the reason, so the decoded answer has
			// to survive the record's own rule.
			submitted := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
			job, err := jobs.New("job_01", "tenant_a", "deepinfra", polled.ID, "videos-generations", submitted)
			require.NoError(t, err)
			require.NoError(t, job.Fail(polled.Reason, submitted.Add(time.Minute)))
			require.NoError(t, job.Validate())
		})
	}
}

// TestAJobRejectionNormalizesLikeAChatRejection keeps the job path inside the
// one failure vocabulary. The retry budget and the availability state read that
// classification, so a job path with its own errors would give the same
// provider rejection two answers.
func TestAJobRejectionNormalizesLikeAChatRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down","type":"rate_limit_error"}}`))
	}))
	defer server.Close()

	connector, err := NewOpenAIConnector(mediaTestConfig(server.URL + "/v1"))
	require.NoError(t, err)
	endpoint := InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/v1"}
	credential := testAPIMaterial("test-key")
	chatFailure := chatCallFailure(t, connector, endpoint, credential)

	_, err = connector.SubmitJob(context.Background(), &JobSubmission{
		MediaTarget: MediaTarget{Model: "provider/video", Endpoint: endpoint, Credential: credential},
		Prompt:      "a boat leaving a harbour",
	})
	normalized := NormalizeFailure("openai", err)
	require.Equal(t, failure.RateLimit, normalized.Kind())
	require.Equal(t, chatFailure.Kind(), normalized.Kind())
	require.Equal(t, chatFailure.Retryable(), normalized.Retryable())
	require.Equal(t, chatFailure.StateScope(), normalized.StateScope())
}

// TestASubmissionThatCannotSucceedNeverReachesTheWire keeps a request that no
// provider would accept from spending a credential and a round trip.
func TestASubmissionThatCannotSucceedNeverReachesTheWire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an invalid job request reached the provider")
	}))
	defer server.Close()

	connector, err := NewOpenAIConnector(mediaTestConfig(server.URL + "/v1"))
	require.NoError(t, err)
	target := MediaTarget{
		Model:      "provider/video",
		Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/v1/videos"},
		Credential: testAPIMaterial("test-key"),
	}

	_, err = connector.SubmitJob(context.Background(), &JobSubmission{MediaTarget: target})
	require.ErrorIs(t, err, ErrInvalidMediaRequest)

	_, err = connector.PollJob(context.Background(), &ProviderJobRef{MediaTarget: target})
	require.ErrorIs(t, err, ErrInvalidMediaRequest)

	_, err = connector.CancelJob(context.Background(), &ProviderJobRef{MediaTarget: target})
	require.ErrorIs(t, err, ErrInvalidMediaRequest)
}

// TestASubmissionSendsOnlyRequestFields keeps the transport facts out of the
// provider body. The endpoint and the credential decide where the call goes and
// who pays for it, and a provider that received either as a field would reject
// the request or record it.
func TestASubmissionSendsOnlyRequestFields(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"v1","status":"queued"}`))
	}))
	defer server.Close()

	connector, err := NewOpenAIConnector(mediaTestConfig(server.URL + "/v1"))
	require.NoError(t, err)
	_, err = connector.SubmitJob(context.Background(), &JobSubmission{
		MediaTarget: MediaTarget{
			Model:      "provider/video",
			Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/v1/videos"},
			Credential: testAPIMaterial("test-key"),
		},
		Prompt:  "a boat leaving a harbour",
		Seconds: "8",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"model":   "provider/video",
		"prompt":  "a boat leaving a harbour",
		"seconds": "8",
	}, body)
}
