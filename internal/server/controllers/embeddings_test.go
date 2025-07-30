package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEmbeddings implements proxy.Proxy for testing
type mockEmbeddings struct {
	embeddings  *proxy.EmbeddingsResponse
	err         error
	lastRequest *proxy.EmbeddingsRequest
}

func (m *mockEmbeddings) ProcessChatCompletion(ctx context.Context, req *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) ProcessChatCompletionStream(ctx context.Context, req *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) ProcessEmbeddings(ctx context.Context, req *proxy.EmbeddingsRequest) (*proxy.EmbeddingsResponse, error) {
	m.lastRequest = req
	return m.embeddings, m.err
}

func (m *mockEmbeddings) ListModels(ctx context.Context) (*proxy.ModelsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) ListProviders(ctx context.Context) (*proxy.ProvidersResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockEmbeddings) GetModelEndpoints(ctx context.Context, modelID string) (*proxy.ModelEndpointsResponse, error) {
	return nil, errors.New("not implemented")
}

func TestEmbeddingsHandler_Create(t *testing.T) {
	tests := []struct {
		name           string
		body           any
		mockResponse   *proxy.EmbeddingsResponse
		mockError      error
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name: "successful embedding generation",
			body: &proxy.EmbeddingsRequest{
				Model: "text-embedding-ada-002",
				Input: "Hello, world!",
			},
			mockResponse: &proxy.EmbeddingsResponse{
				Object: "list",
				Data: []connectors.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: []float32{0.1, 0.2, 0.3},
					},
				},
				Model: "text-embedding-ada-002",
				Usage: &connectors.Usage{
					PromptTokens: 3,
					TotalTokens:  3,
				},
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var resp proxy.EmbeddingsResponse
				err := json.Unmarshal(body, &resp)
				require.NoError(t, err)
				assert.Equal(t, "list", resp.Object)
				assert.Len(t, resp.Data, 1)
				assert.Equal(t, "text-embedding-ada-002", resp.Model)
			},
		},
		{
			name:           "invalid request body",
			body:           "invalid json",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp map[string]any
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["error"].(map[string]any)["message"], "Invalid request body")
			},
		},
		{
			name: "validation error",
			body: &proxy.EmbeddingsRequest{
				Model: "text-embedding-ada-002",
				Input: "test",
			},
			mockError:      &proxy.ValidationError{Field: "model", Message: "model not supported"},
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp map[string]any
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["error"].(map[string]any)["message"], "model not supported")
			},
		},
		{
			name: "provider error",
			body: &proxy.EmbeddingsRequest{
				Model: "text-embedding-ada-002",
				Input: "test",
			},
			mockError:      &proxy.ProviderError{Provider: "openai", Code: "rate_limit", Message: "rate limit exceeded"},
			expectedStatus: http.StatusTooManyRequests,
		},
		{
			name: "embeddings not supported",
			body: &proxy.EmbeddingsRequest{
				Model: "gpt-4",
				Input: "test",
			},
			mockError:      proxy.ErrEmbeddingsNotSupported,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock service
			mockService := &mockEmbeddings{
				embeddings: tt.mockResponse,
				err:        tt.mockError,
			}

			// Create handler
			handler := NewEmbeddingsController(mockService)

			// Create request
			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/v1/embeddings", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")

			// Add context values
			ctx := context.WithValue(req.Context(), "api_key", "test-key")
			ctx = context.WithValue(ctx, "request_id", "test-request-id")
			req = req.WithContext(ctx)

			// Create response recorder
			w := httptest.NewRecorder()

			// Handle request
			handler.Create(w, req)

			// Check status
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Check response
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}

			// Check that context was passed to service
			if tt.mockError == nil && mockService.lastRequest != nil {
				assert.Equal(t, "test-key", mockService.lastRequest.APIKey)
				assert.Equal(t, "test-request-id", mockService.lastRequest.RequestID)
			}
		})
	}
}

func TestEmbeddingsHandler_CacheHeaders(t *testing.T) {
	handler := NewEmbeddingsController(nil)

	// Test cache hit
	t.Run("cache hit", func(t *testing.T) {
		// Create mock service that returns response with cache status
		mockService := &mockEmbeddings{
			embeddings: &proxy.EmbeddingsResponse{
				Object:      "list",
				Data:        []connectors.Embedding{},
				Model:       "text-embedding-ada-002",
				Usage:       &connectors.Usage{},
				CacheStatus: "HIT",
			},
		}
		handler.service = mockService

		body := &proxy.EmbeddingsRequest{
			Model: "text-embedding-ada-002",
			Input: "test",
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/v1/embeddings", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")

		// Add required context values
		ctx := context.WithValue(req.Context(), "request_id", "test-request-id")
		ctx = context.WithValue(ctx, "api_key", "test-key")
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		handler.Create(w, req)

		assert.Equal(t, "HIT", w.Header().Get("X-Cache"))
	})

	// Test cache miss
	t.Run("cache miss", func(t *testing.T) {
		// Create mock service that returns response with cache status
		mockService := &mockEmbeddings{
			embeddings: &proxy.EmbeddingsResponse{
				Object:      "list",
				Data:        []connectors.Embedding{},
				Model:       "text-embedding-ada-002",
				Usage:       &connectors.Usage{},
				CacheStatus: "MISS",
			},
		}
		handler.service = mockService

		body := &proxy.EmbeddingsRequest{
			Model: "text-embedding-ada-002",
			Input: "test",
		}
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/v1/embeddings", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")

		// Add required context values
		ctx := context.WithValue(req.Context(), "request_id", "test-request-id")
		ctx = context.WithValue(ctx, "api_key", "test-key")
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		handler.Create(w, req)

		assert.Equal(t, "MISS", w.Header().Get("X-Cache"))
	})
}
