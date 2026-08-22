package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/stretchr/testify/require"
)

type mockProxy struct {
	chat     *proxy.ChatCompletionResponse
	stream   proxy.ChatCompletionStreamResponse
	err      error
	lastChat *proxy.ChatCompletionRequest
}

func (m *mockProxy) ProcessChatCompletion(_ context.Context, request *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	m.lastChat = request
	return m.chat, m.err
}

func (m *mockProxy) ProcessChatCompletionStream(_ context.Context, request *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
	m.lastChat = request
	return m.stream, m.err
}

func (m *mockProxy) ProcessEmbeddings(context.Context, *proxy.EmbeddingsRequest) (*proxy.EmbeddingsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProxy) ListModels(context.Context) (*proxy.ModelsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProxy) ListProviders(context.Context) (*proxy.ProvidersResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProxy) ListAuthors(context.Context) (*proxy.AuthorsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProxy) GetAuthor(context.Context, string) (*proxy.AuthorInfo, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProxy) GetModelEndpoints(context.Context, string) (*proxy.ModelEndpointsResponse, error) {
	return nil, errors.New("not implemented")
}

func TestChatControllerOpenAIContract(t *testing.T) {
	service := &mockProxy{chat: chatFixture()}
	controller := controllers.NewChatController(service)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":100}`,
	))
	ctx := requestctx.WithAPIKey(request.Context(), "test-key")
	ctx = requestctx.WithAPIKeyModel(ctx, &identity.APIKey{AllowedModels: []string{"openai/gpt-4.1"}})
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "chatcmpl-test", decodeJSON(t, recorder.Body.Bytes())["id"])
	require.Equal(t, "openai/gpt-4.1", service.lastChat.Request.Model)
	require.Equal(t, 100, *service.lastChat.Request.Sampling.MaxTokens)
	require.Equal(t, "hello", service.lastChat.Request.Messages[0].Content[0].Text)
	require.Equal(t, []string{"openai/gpt-4.1"}, service.lastChat.APIKeyConfig.AllowedModels)
}

func TestChatControllerOpenRouterRoutingContract(t *testing.T) {
	service := &mockProxy{chat: chatFixture()}
	controller := controllers.NewOpenRouterChatController(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completions", bytes.NewBufferString(
		`{"models":["openai/gpt-4.1","anthropic/claude-sonnet-4"],"messages":[{"role":"user","content":"hello"}],"provider":{"order":["openai"],"allow_fallbacks":true},"route":"fallback"}`,
	))
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	response := decodeJSON(t, recorder.Body.Bytes())
	require.Equal(t, "openai", response["provider"])
	require.Equal(t, []string{"openai/gpt-4.1", "anthropic/claude-sonnet-4"}, service.lastChat.Request.FallbackModels)
	require.Equal(t, []string{"openai"}, service.lastChat.Provider.Order)
	require.True(t, service.lastChat.Provider.AllowFallback)
	require.Equal(t, "fallback", service.lastChat.Route)
}

func TestUnenforcedProviderFieldsHeader(t *testing.T) {
	service := &mockProxy{chat: chatFixture()}
	controller := controllers.NewOpenRouterChatController(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completions", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}],`+
			`"provider":{"quantizations":["fp8"],"zdr":true,"sort":"price","max_price":{"prompt":2,"request":0.01}}}`,
	))
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "max_price.request,quantizations,zdr",
		recorder.Header().Get("X-Starport-Unenforced-Provider-Fields"))
	require.Equal(t, "price", service.lastChat.Provider.Sort)
	require.InDelta(t, 2.0, service.lastChat.Provider.MaxPromptPricePer1M, 1e-9)

	// Enforced-only requests carry no unenforced-fields header.
	request = httptest.NewRequest(http.MethodPost, "/api/v1/chat/completions", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}],"provider":{"only":["openai"]}}`,
	))
	recorder = httptest.NewRecorder()
	controller.Create(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Header().Get("X-Starport-Unenforced-Provider-Fields"))
}

func TestChatControllerOpenRouterStreamErrorContract(t *testing.T) {
	service := &mockProxy{stream: &eventStream{
		events: []inference.StreamEvent{{
			Kind: inference.StreamDelta, ID: "chatcmpl-stream", Model: "openai/gpt-4.1",
			ModelUsed: "openai/gpt-4.1", Deltas: []inference.ChoiceDelta{{Index: 0, Text: "partial"}},
		}},
		err: errors.New("provider disconnected"),
	}}
	controller := controllers.NewOpenRouterChatController(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completions", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}],"stream":true}`,
	))
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), `"content":"partial"`)
	require.Contains(t, recorder.Body.String(), `"error":{"code":502`)
	require.Contains(t, recorder.Body.String(), `"finish_reason":"error"`)
}

func TestChatControllerRejectsUnknownOpenAIField(t *testing.T) {
	controller := controllers.NewChatController(&mockProxy{})
	recorder := httptest.NewRecorder()
	controller.Create(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[],"provider":{"only":["openai"]}}`,
	)))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"type":"invalid_request_error"`)
}

func chatFixture() *proxy.ChatCompletionResponse {
	return &proxy.ChatCompletionResponse{Response: inference.ChatResponse{
		ID: "chatcmpl-test", CreatedUnix: 1744329600, Model: "openai/gpt-4.1", ModelUsed: "openai/gpt-4.1",
		Choices: []inference.Choice{{
			Index: 0, FinishReason: "stop",
			Message: inference.Message{Role: inference.RoleAssistant, Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}}},
		}},
		Usage: inference.Usage{InputTokens: 5, OutputTokens: 1, TotalTokens: 6},
	}}
}

func decodeJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

type eventStream struct {
	events []inference.StreamEvent
	index  int
	err    error
}

func (s *eventStream) Read() (*inference.StreamEvent, error) {
	if s.index >= len(s.events) {
		if s.err != nil {
			err := s.err
			s.err = nil
			return nil, err
		}
		return nil, io.EOF
	}
	event := s.events[s.index].Clone()
	s.index++
	return &event, nil
}

func (s *eventStream) Close() error { return nil }
