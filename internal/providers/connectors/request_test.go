package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRequestWritesExtensionsAtTopLevel(t *testing.T) {
	body, err := marshalOpenAIChatRequest(&ChatRequest{
		Model:           "opaque-model",
		Messages:        []Message{{Role: RoleUser, Content: "hello"}},
		ProviderOptions: map[string]any{"top_k": 40},
	})
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	require.JSONEq(t, `40`, string(fields["top_k"]))
	require.NotContains(t, fields, "provider_options")

	_, err = marshalOpenAIChatRequest(&ChatRequest{
		Model:           "opaque-model",
		Messages:        []Message{{Role: RoleUser, Content: "hello"}},
		ProviderOptions: map[string]any{"stream": true},
	})
	require.ErrorContains(t, err, "conflicts with a canonical OpenAI field")
}

func TestProviderHTTPRequestIsOneLogicalAttempt(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	response, err := doRequest(server.Client(), request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.EqualValues(t, 1, calls.Load())
}
