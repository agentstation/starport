package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockModelsService implements the proxy.Service interface for testing
type mockModelsService struct {
	models    *proxy.ModelsResponse
	endpoints *proxy.ModelEndpointsResponse
	err       error
}

func (m *mockModelsService) ProcessChatCompletion(ctx context.Context, req *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModelsService) ProcessChatCompletionStream(ctx context.Context, req *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModelsService) ProcessEmbeddings(ctx context.Context, req *proxy.EmbeddingsRequest) (*proxy.EmbeddingsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModelsService) ListModels(ctx context.Context) (*proxy.ModelsResponse, error) {
	return m.models, m.err
}

func (m *mockModelsService) ListProviders(ctx context.Context) (*proxy.ProvidersResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockModelsService) GetModelEndpoints(ctx context.Context, modelID string) (*proxy.ModelEndpointsResponse, error) {
	return m.endpoints, m.err
}

func TestModelsHandler_List(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		mockResponse   *proxy.ModelsResponse
		mockError      error
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name: "list models with basic format (/v1/models)",
			path: "/v1/models",
			mockResponse: &proxy.ModelsResponse{
				Object: "list",
				Data: []proxy.ModelInfo{
					{
						ID:      "gpt-4",
						Object:  "model",
						Created: 1234567890,
						OwnedBy: "openai",
						// Enhanced fields that should be stripped
						Pricing: &proxy.ModelPricing{
							Prompt:     "0.03",
							Completion: "0.06",
							Currency:   "USD",
						},
						Context: intPtr(8192),
					},
					{
						ID:      "claude-3-opus",
						Object:  "model",
						Created: 1234567891,
						OwnedBy: "anthropic",
					},
				},
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				// Should return basic OpenAI format
				var resp map[string]interface{}
				err := json.Unmarshal(body, &resp)
				require.NoError(t, err)

				assert.Equal(t, "list", resp["object"])
				data := resp["data"].([]interface{})
				assert.Len(t, data, 2)

				// Check that enhanced fields are not present
				model1 := data[0].(map[string]interface{})
				assert.Equal(t, "gpt-4", model1["id"])
				assert.NotContains(t, model1, "pricing")
				assert.NotContains(t, model1, "context_length")
			},
		},
		{
			name: "list models with enhanced format (/api/v1/models)",
			path: "/api/v1/models",
			mockResponse: &proxy.ModelsResponse{
				Object: "list",
				Data: []proxy.ModelInfo{
					{
						ID:      "gpt-4",
						Object:  "model",
						Created: 1234567890,
						OwnedBy: "openai",
						Pricing: &proxy.ModelPricing{
							Prompt:     "0.03",
							Completion: "0.06",
							Currency:   "USD",
						},
						Context: intPtr(8192),
						Type:    "language",
					},
				},
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				// Should return enhanced format with metadata
				var resp proxy.ModelsResponse
				err := json.Unmarshal(body, &resp)
				require.NoError(t, err)

				assert.Equal(t, "list", resp.Object)
				assert.Len(t, resp.Data, 1)

				// Check that enhanced fields are present
				model := resp.Data[0]
				assert.NotNil(t, model.Pricing)
				assert.NotNil(t, model.Context)
				assert.NotEmpty(t, model.Type)
			},
		},
		{
			name:           "service error",
			path:           "/v1/models",
			mockError:      errors.New("service unavailable"),
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "cache headers",
			path: "/v1/models",
			mockResponse: &proxy.ModelsResponse{
				Object: "list",
				Data:   []proxy.ModelInfo{},
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				// Response body check handled by the test setup
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create response copy for cache test
			mockResp := tt.mockResponse
			if tt.name == "cache headers" && mockResp != nil {
				// Create a new response with cache status
				mockResp = &proxy.ModelsResponse{
					Object:      tt.mockResponse.Object,
					Data:        tt.mockResponse.Data,
					CacheStatus: "HIT",
				}
			}

			// Create mock service
			mockService := &mockModelsService{
				models: mockResp,
				err:    tt.mockError,
			}

			// Create handler
			handler := NewModelsHandler(mockService)

			// Create request
			req := httptest.NewRequest("GET", tt.path, nil)

			// Create response recorder
			w := httptest.NewRecorder()

			// Handle request
			handler.List(w, req)

			// Check status
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Check response
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}

			// Check cache header for cache test
			if tt.name == "cache headers" {
				assert.Equal(t, "HIT", w.Header().Get("X-Cache"))
			}
		})
	}
}

func TestModelsHandler_Get(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		modelID        string
		mockResponse   *proxy.ModelsResponse
		mockError      error
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:    "get specific model basic format",
			path:    "/v1/models/gpt-4",
			modelID: "gpt-4",
			mockResponse: &proxy.ModelsResponse{
				Object: "list",
				Data: []proxy.ModelInfo{
					{
						ID:      "gpt-4",
						Object:  "model",
						Created: 1234567890,
						OwnedBy: "openai",
						Pricing: &proxy.ModelPricing{
							Prompt:     "0.03",
							Completion: "0.06",
							Currency:   "USD",
						},
					},
					{
						ID:      "gpt-3.5-turbo",
						Object:  "model",
						Created: 1234567891,
						OwnedBy: "openai",
					},
				},
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var model map[string]interface{}
				err := json.Unmarshal(body, &model)
				require.NoError(t, err)

				assert.Equal(t, "gpt-4", model["id"])
				assert.NotContains(t, model, "pricing")
			},
		},
		{
			name:    "get specific model enhanced format",
			path:    "/api/v1/models/gpt-4",
			modelID: "gpt-4",
			mockResponse: &proxy.ModelsResponse{
				Object: "list",
				Data: []proxy.ModelInfo{
					{
						ID:      "gpt-4",
						Object:  "model",
						Created: 1234567890,
						OwnedBy: "openai",
						Pricing: &proxy.ModelPricing{
							Prompt:     "0.03",
							Completion: "0.06",
							Currency:   "USD",
						},
					},
				},
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var model proxy.ModelInfo
				err := json.Unmarshal(body, &model)
				require.NoError(t, err)

				assert.Equal(t, "gpt-4", model.ID)
				assert.NotNil(t, model.Pricing)
			},
		},
		{
			name:    "model not found",
			path:    "/v1/models/nonexistent",
			modelID: "nonexistent",
			mockResponse: &proxy.ModelsResponse{
				Object: "list",
				Data: []proxy.ModelInfo{
					{
						ID:      "gpt-4",
						Object:  "model",
						Created: 1234567890,
						OwnedBy: "openai",
					},
				},
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:    "URL encoded model ID",
			path:    "/v1/models/anthropic%2Fclaude-3-opus",
			modelID: "anthropic/claude-3-opus",
			mockResponse: &proxy.ModelsResponse{
				Object: "list",
				Data: []proxy.ModelInfo{
					{
						ID:      "anthropic/claude-3-opus",
						Object:  "model",
						Created: 1234567890,
						OwnedBy: "anthropic",
					},
				},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "missing model ID",
			path:           "/v1/models/",
			modelID:        "",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock service
			mockService := &mockModelsService{
				models: tt.mockResponse,
				err:    tt.mockError,
			}

			// Create handler
			handler := NewModelsHandler(mockService)

			// Create router to handle URL params
			r := chi.NewRouter()
			r.Get("/v1/models/{model}", handler.Get)
			r.Get("/api/v1/models/{model}", handler.Get)

			// Create request
			req := httptest.NewRequest("GET", tt.path, nil)

			// Create response recorder
			w := httptest.NewRecorder()

			// Handle request
			r.ServeHTTP(w, req)

			// Check status
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Check response
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

func TestModelsHandler_GetEndpoints(t *testing.T) {
	tests := []struct {
		name           string
		modelID        string
		mockResponse   *proxy.ModelEndpointsResponse
		mockError      error
		expectedStatus int
	}{
		{
			name:    "successful endpoints retrieval",
			modelID: "gpt-4",
			mockResponse: &proxy.ModelEndpointsResponse{
				Model: "gpt-4",
				Endpoints: []proxy.EndpointInfo{
					{
						Provider:  "openai",
						Endpoint:  "/api/v1/chat/completions",
						Available: true,
					},
				},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:    "URL encoded model ID",
			modelID: "anthropic%2Fclaude-3-opus",
			mockResponse: &proxy.ModelEndpointsResponse{
				Model: "anthropic/claude-3-opus",
				Endpoints: []proxy.EndpointInfo{
					{
						Provider:  "anthropic",
						Endpoint:  "/api/v1/chat/completions",
						Available: true,
					},
				},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "missing model ID",
			modelID:        "",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "service error",
			modelID:        "gpt-4",
			mockError:      errors.New("service error"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock service
			mockService := &mockModelsService{
				endpoints: tt.mockResponse,
				err:       tt.mockError,
			}

			// Create handler
			handler := NewModelsHandler(mockService)

			// Create router to handle URL params
			r := chi.NewRouter()
			r.Get("/api/v1/models/{model}/endpoints", handler.GetEndpoints)

			// Create request
			path := "/api/v1/models/" + tt.modelID + "/endpoints"
			req := httptest.NewRequest("GET", path, nil)

			// Create response recorder
			w := httptest.NewRecorder()

			// Handle request
			r.ServeHTTP(w, req)

			// Check status
			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

func intPtr(i int) *int {
	return &i
}
