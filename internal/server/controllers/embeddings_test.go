package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/stretchr/testify/require"
)

type mockEmbeddings struct {
	unsupportedMedia
	response    *proxy.EmbeddingsResponse
	err         error
	lastRequest *proxy.EmbeddingsRequest
}

func (m *mockEmbeddings) ProcessChatCompletion(context.Context, *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) ProcessChatCompletionStream(context.Context, *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) GetLogo(context.Context, view.LogoKind, string) ([]byte, error) {
	return nil, &proxy.ProviderError{Code: "not_found", Message: "Logo not found"}
}

func (m *mockEmbeddings) ProcessEmbeddings(_ context.Context, request *proxy.EmbeddingsRequest) (*proxy.EmbeddingsResponse, error) {
	m.lastRequest = request
	return m.response, m.err
}

func (m *mockEmbeddings) ListModels(context.Context) (*proxy.ModelsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) ListProviders(context.Context) (*proxy.ProvidersResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) ListAuthors(context.Context) (*proxy.AuthorsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) GetAuthor(context.Context, string) (*proxy.AuthorInfo, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) GetModelEndpoints(context.Context, string) (*proxy.ModelEndpointsResponse, error) {
	return nil, errors.New("not implemented")
}

func TestEmbeddingsControllerOpenAIContract(t *testing.T) {
	service := &mockEmbeddings{response: &proxy.EmbeddingsResponse{
		Response: inference.EmbeddingResponse{
			Model: "openai/text-embedding-3-small",
			Data:  []inference.Embedding{{Index: 0, Vector: []float32{0.1, 0.2}}},
			Usage: inference.Usage{InputTokens: 2, TotalTokens: 2},
		},
		CacheStatus: proxy.CacheStatusHit,
	}}
	controller := NewEmbeddingsController(service)
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewBufferString(
		`{"model":"openai/text-embedding-3-small","input":"hello"}`,
	))
	ctx := requestctx.WithAPIKey(request.Context(), "test-key")
	ctx = requestctx.WithAPIKeyModel(ctx, &identity.APIKey{AllowedModels: []string{"openai/text-embedding-3-small"}})
	ctx = context.WithValue(ctx, middleware.RequestIDKey, "request-1")
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "HIT", recorder.Header().Get("X-Cache"))
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "list", response["object"])
	require.Equal(t, "openai/text-embedding-3-small", service.lastRequest.Request.Model)
	require.Equal(t, []string{"hello"}, service.lastRequest.Request.Input.Texts)
	require.Equal(t, "test-key", service.lastRequest.APIKey)
	require.Equal(t, []string{"openai/text-embedding-3-small"}, service.lastRequest.APIKeyConfig.AllowedModels)
	require.Equal(t, "request-1", service.lastRequest.RequestID)
	require.Equal(t, string(ProtocolOpenAI), service.lastRequest.Protocol)
}

func TestEmbeddingsControllerOpenRouterContract(t *testing.T) {
	service := &mockEmbeddings{response: &proxy.EmbeddingsResponse{Response: inference.EmbeddingResponse{
		Model: "openai/text-embedding-3-small",
		Data:  []inference.Embedding{{Index: 0, Vector: []float32{0.1}}},
	}}}
	controller := NewOpenRouterEmbeddingsController(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/embeddings", bytes.NewBufferString(
		`{"model":"openai/text-embedding-3-small","input":[[1,2,3]]}`,
	))
	recorder := httptest.NewRecorder()

	controller.Create(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"provider":"openai"`)
	require.Equal(t, [][]int{{1, 2, 3}}, service.lastRequest.Request.Input.TokenIDs)
}

func TestEmbeddingsControllerRejectsInvalidJSONInSelectedDialect(t *testing.T) {
	controller := NewOpenRouterEmbeddingsController(&mockEmbeddings{})
	recorder := httptest.NewRecorder()
	controller.Create(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/embeddings", bytes.NewBufferString(`{"unknown":true}`)))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	errorValue := response["error"].(map[string]any)
	require.Equal(t, float64(http.StatusBadRequest), errorValue["code"])
}
