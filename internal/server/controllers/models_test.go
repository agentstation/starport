package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

type mockModels struct {
	models *proxy.ModelsResponse
	err    error
}

func (m *mockModels) ProcessChatCompletion(context.Context, *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModels) ProcessChatCompletionStream(context.Context, *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModels) ProcessEmbeddings(context.Context, *proxy.EmbeddingsRequest) (*proxy.EmbeddingsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModels) ListModels(context.Context) (*proxy.ModelsResponse, error) {
	return m.models, m.err
}

func (m *mockModels) ListProviders(context.Context) (*proxy.ProvidersResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModels) ListAuthors(context.Context) (*proxy.AuthorsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModels) GetAuthor(context.Context, string) (*proxy.AuthorInfo, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModels) GetModelEndpoints(_ context.Context, model string) (*proxy.ModelEndpointsResponse, error) {
	return &proxy.ModelEndpointsResponse{Model: model}, m.err
}

func TestModelsControllerSelectsOpenAIShape(t *testing.T) {
	controller := NewModelsController(&mockModels{models: modelFixture()})
	recorder := httptest.NewRecorder()
	controller.List(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "list", response["object"])
	model := response["data"].([]any)[0].(map[string]any)
	require.NotContains(t, model, "pricing")
	require.NotContains(t, response, "total_count")
}

func TestModelsControllerSelectsOpenRouterShape(t *testing.T) {
	controller := NewOpenRouterModelsController(&mockModels{models: modelFixture()})
	recorder := httptest.NewRecorder()
	controller.List(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/models", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, float64(1), response["total_count"])
	require.Contains(t, response, "links")
	model := response["data"].([]any)[0].(map[string]any)
	require.Contains(t, model, "pricing")
	require.Equal(t, float64(128000), model["context_length"])
}

func TestOpenRouterModelListCarriesEveryOffering(t *testing.T) {
	models := modelFixture()
	models.Data[0].Authors = []proxy.ModelAuthorInfo{{ID: "openai", Name: "OpenAI"}}
	models.Data[0].Offerings = []proxy.ModelOfferingInfo{
		{
			Provider: "azure-openai", ProviderModelID: "gpt-4.1",
			Availability: "available", Lifecycle: "active",
			Pricing: &proxy.OfferingPricingInfo{Prompt: "0.000002", CacheRead: "0.0000005", Currency: "USD"},
		},
		{Provider: "openai", ProviderModelID: "gpt-4.1", Availability: "available"},
	}
	controller := NewOpenRouterModelsController(&mockModels{models: models})
	recorder := httptest.NewRecorder()
	controller.List(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/models", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	model := response["data"].([]any)[0].(map[string]any)
	require.Equal(t, "OpenAI", model["authors"].([]any)[0].(map[string]any)["name"])
	offerings := model["offerings"].([]any)
	require.Len(t, offerings, 2, "every offering must survive the codec")
	first := offerings[0].(map[string]any)
	require.Equal(t, "azure-openai", first["provider"])
	require.Equal(t, "0.0000005", first["pricing"].(map[string]any)["cache_read"])
	require.Equal(t, "openai", offerings[1].(map[string]any)["provider"])
}

func TestModelsControllerGetsEncodedModelID(t *testing.T) {
	controller := NewOpenRouterModelsController(&mockModels{models: modelFixture()})
	router := chi.NewRouter()
	router.Get("/api/v1/models/{model}", controller.Get)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/models/openai%2Fgpt-4.1", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"id":"openai/gpt-4.1"`)
}

func modelFixture() *proxy.ModelsResponse {
	contextLength := 128000
	return &proxy.ModelsResponse{Object: "list", Data: []proxy.ModelInfo{{
		ID: "openai/gpt-4.1", CanonicalSlug: "openai/gpt-4.1", Name: "GPT-4.1",
		Object: "model", Created: 1744329600, OwnedBy: "openai", Context: &contextLength,
		Pricing:             &proxy.ModelPricing{Prompt: "0.000002", Completion: "0.000008", Currency: "USD"},
		SupportedParameters: []string{"tools", "response_format"},
	}}}
}
