package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starport/internal/inference"
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

// A caller that forbids several tool calls in one assistant turn has to have
// that reach the provider. Starport decoded the field and then dropped it, so
// a model could answer with parallel tool calls the caller cannot execute.
func TestOpenAIRequestCarriesParallelToolCallPolicy(t *testing.T) {
	messages := []inference.Message{{
		Role:    inference.RoleUser,
		Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
	}}
	tools := []inference.Tool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	forbid := false

	wire, err := ChatRequestFromInference(inference.ChatRequest{
		Model: "opaque-model", Messages: messages, Tools: tools,
		ParallelToolCalls: &forbid,
	})
	require.NoError(t, err)
	body, err := marshalOpenAIChatRequest(wire)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	require.JSONEq(t, `false`, string(fields["parallel_tool_calls"]))

	// An unset policy leaves the provider default in place. Sending a value
	// the caller never chose is its own defect.
	wire, err = ChatRequestFromInference(inference.ChatRequest{
		Model: "opaque-model", Messages: messages, Tools: tools,
	})
	require.NoError(t, err)
	body, err = marshalOpenAIChatRequest(wire)
	require.NoError(t, err)
	fields = nil
	require.NoError(t, json.Unmarshal(body, &fields))
	require.NotContains(t, fields, "parallel_tool_calls")

	// The field is canonical now, so a provider option must not shadow it.
	_, err = marshalOpenAIChatRequest(&ChatRequest{
		Model:           "opaque-model",
		Messages:        []Message{{Role: RoleUser, Content: "hello"}},
		ProviderOptions: map[string]any{"parallel_tool_calls": true},
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
