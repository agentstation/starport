package controllers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/server/controllers"
)

func TestResponsesControllerServesTheStatelessSubset(t *testing.T) {
	service := &mockProxy{chat: chatFixture()}
	controller := controllers.NewResponsesController(service)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","input":"hello","max_output_tokens":100}`,
	))
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	response := decodeJSON(t, recorder.Body.Bytes())
	require.Equal(t, "response", response["object"])
	require.Equal(t, "completed", response["status"])
	output := response["output"].([]any)
	require.Len(t, output, 1)
	message := output[0].(map[string]any)
	require.Equal(t, "message", message["type"])
	content := message["content"].([]any)[0].(map[string]any)
	require.Equal(t, "output_text", content["type"])
	require.Equal(t, "hello", content["text"])

	require.Equal(t, "openai/gpt-4.1", service.lastChat.Request.Model)
	require.Equal(t, 100, *service.lastChat.Request.Sampling.MaxTokens)
	require.Equal(t, "hello", service.lastChat.Request.Messages[0].Content[0].Text)
	require.Equal(t, "openai", service.lastChat.Protocol)
}

func TestResponsesControllerRefusesStoredStateWithTheNamedParam(t *testing.T) {
	controller := controllers.NewResponsesController(&mockProxy{})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","input":"hello","previous_response_id":"resp_0"}`,
	))
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	body := recorder.Body.String()
	require.Contains(t, body, `"type":"invalid_request_error"`)
	require.Contains(t, body, `"param":"previous_response_id"`)
	require.Contains(t, body, "stores none")
}

func TestResponsesControllerStreamsTheNamedEventSequence(t *testing.T) {
	service := &mockProxy{stream: &eventStream{events: []inference.StreamEvent{
		{
			Kind: inference.StreamStart, ID: "resp_stream", CreatedUnix: 36,
			Model:  "openai/gpt-4.1",
			Deltas: []inference.ChoiceDelta{{Index: 0, Role: inference.RoleAssistant}},
		},
		{
			Kind: inference.StreamDelta, ID: "resp_stream",
			Deltas: []inference.ChoiceDelta{{Index: 0, Text: "hello"}},
		},
		{
			Kind: inference.StreamEnd, ID: "resp_stream",
			Deltas: []inference.ChoiceDelta{{Index: 0, FinishReason: "stop"}},
		},
	}}}
	controller := controllers.NewResponsesController(service)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","input":"hello","stream":true}`,
	))
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	body := recorder.Body.String()
	require.Contains(t, body, "event: response.created\n")
	require.Contains(t, body, "event: response.output_text.delta\n")
	require.Contains(t, body, `"delta":"hello"`)
	require.Contains(t, body, "event: response.completed\n")
	require.NotContains(t, body, "[DONE]",
		"a Responses stream ends with its terminal event, not the chat marker")
}
