package controllers_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/stretchr/testify/require"
)

// Every proxied chat response states the gateway-added latency in the
// x-starport-overhead-ms header, on success, on failure, and on streams.

func requireOverheadHeader(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	value := recorder.Header().Get(proxy.OverheadHeader)
	require.NotEmpty(t, value, "overhead header must be present")
	overhead, err := strconv.ParseInt(value, 10, 64)
	require.NoError(t, err)
	require.GreaterOrEqual(t, overhead, int64(0))
}

func TestChatControllerOverheadHeaderNonStream(t *testing.T) {
	controller := controllers.NewChatController(&mockProxy{chat: chatFixture()})
	recorder := httptest.NewRecorder()
	controller.Create(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}]}`,
	)))

	require.Equal(t, http.StatusOK, recorder.Code)
	requireOverheadHeader(t, recorder)
}

func TestChatControllerOverheadHeaderNonStreamError(t *testing.T) {
	controller := controllers.NewChatController(&mockProxy{err: errors.New("provider exploded")})
	recorder := httptest.NewRecorder()
	controller.Create(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}]}`,
	)))

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	requireOverheadHeader(t, recorder)
}

func TestChatControllerOverheadHeaderStream(t *testing.T) {
	controller := controllers.NewChatController(&mockProxy{stream: &eventStream{
		events: []inference.StreamEvent{{
			Kind: inference.StreamDelta, ID: "chatcmpl-stream", Model: "openai/gpt-4.1",
			ModelUsed: "openai/gpt-4.1", Deltas: []inference.ChoiceDelta{{Index: 0, Text: "hi"}},
		}},
	}})
	recorder := httptest.NewRecorder()
	controller.Create(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}],"stream":true}`,
	)))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	requireOverheadHeader(t, recorder)
}
