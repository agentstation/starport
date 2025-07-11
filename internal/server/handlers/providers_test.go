package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvidersService implements the proxy.Service interface for testing
type mockProvidersService struct {
	providers *proxy.ProvidersResponse
	err       error
}

func (m *mockProvidersService) ProcessChatCompletion(ctx context.Context, req *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProvidersService) ProcessChatCompletionStream(ctx context.Context, req *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProvidersService) ProcessEmbeddings(ctx context.Context, req *proxy.EmbeddingsRequest) (*proxy.EmbeddingsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProvidersService) ListModels(ctx context.Context) (*proxy.ModelsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProvidersService) ListProviders(ctx context.Context) (*proxy.ProvidersResponse, error) {
	return m.providers, m.err
}

func (m *mockProvidersService) GetModelEndpoints(ctx context.Context, modelID string) (*proxy.ModelEndpointsResponse, error) {
	return nil, errors.New("not implemented")
}

func TestProvidersHandler_List(t *testing.T) {
	tests := []struct {
		name           string
		mockResponse   *proxy.ProvidersResponse
		mockError      error
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name: "successful provider listing",
			mockResponse: &proxy.ProvidersResponse{
				Providers: []proxy.ProviderInfo{
					{
						ID:           "openai",
						Name:         "OpenAI",
						Description:  "Provider: OpenAI",
						URL:          "https://openai.com",
						RequiresAuth: true,
					},
					{
						ID:           "anthropic",
						Name:         "Anthropic",
						Description:  "Provider: Anthropic",
						URL:          "https://anthropic.com",
						RequiresAuth: true,
					},
				},
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var resp proxy.ProvidersResponse
				err := json.Unmarshal(body, &resp)
				require.NoError(t, err)

				assert.Len(t, resp.Providers, 2)

				// Check first provider
				assert.Equal(t, "openai", resp.Providers[0].ID)
				assert.Equal(t, "OpenAI", resp.Providers[0].Name)
				assert.True(t, resp.Providers[0].RequiresAuth)

				// Check second provider
				assert.Equal(t, "anthropic", resp.Providers[1].ID)
				assert.Equal(t, "Anthropic", resp.Providers[1].Name)
			},
		},
		{
			name: "empty provider list",
			mockResponse: &proxy.ProvidersResponse{
				Providers: []proxy.ProviderInfo{},
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var resp proxy.ProvidersResponse
				err := json.Unmarshal(body, &resp)
				require.NoError(t, err)

				assert.Empty(t, resp.Providers)
			},
		},
		{
			name:           "service error",
			mockError:      errors.New("service unavailable"),
			expectedStatus: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp map[string]interface{}
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)

				assert.Contains(t, errResp, "error")
				errorDetails := errResp["error"].(map[string]interface{})
				assert.Equal(t, "server_error", errorDetails["type"])
			},
		},
		{
			name:           "provider error",
			mockError:      &proxy.ProviderError{Provider: "test", Code: "auth_error", Message: "authentication failed"},
			expectedStatus: http.StatusUnauthorized,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp map[string]interface{}
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)

				assert.Contains(t, errResp, "error")
				errorDetails := errResp["error"].(map[string]interface{})
				assert.Equal(t, "authentication_error", errorDetails["type"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock service
			mockService := &mockProvidersService{
				providers: tt.mockResponse,
				err:       tt.mockError,
			}

			// Create handler
			handler := NewProvidersHandler(mockService)

			// Create request
			req := httptest.NewRequest("GET", "/api/v1/providers", nil)

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
		})
	}
}

func TestProvidersHandler_CacheHeaders(t *testing.T) {
	tests := []struct {
		name        string
		cacheStatus string
	}{
		{
			name:        "cache hit",
			cacheStatus: "HIT",
		},
		{
			name:        "cache miss",
			cacheStatus: "MISS",
		},
		{
			name:        "no cache status",
			cacheStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock service
			mockService := &mockProvidersService{
				providers: &proxy.ProvidersResponse{
					Providers: []proxy.ProviderInfo{},
				},
			}

			// Create handler
			handler := NewProvidersHandler(mockService)

			// Create request
			req := httptest.NewRequest("GET", "/api/v1/providers", nil)

			// Add cache status to context if provided
			if tt.cacheStatus != "" {
				ctx := context.WithValue(req.Context(), "X-Cache", tt.cacheStatus)
				req = req.WithContext(ctx)
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Handle request
			handler.List(w, req)

			// Check status
			assert.Equal(t, http.StatusOK, w.Code)

			// Check cache header
			if tt.cacheStatus != "" {
				assert.Equal(t, tt.cacheStatus, w.Header().Get("X-Cache"))
			} else {
				assert.Empty(t, w.Header().Get("X-Cache"))
			}
		})
	}
}

func TestProvidersHandler_ErrorHandling(t *testing.T) {
	tests := []struct {
		name           string
		mockError      error
		expectedStatus int
		expectedType   string
	}{
		{
			name:           "validation error",
			mockError:      &proxy.ValidationError{Field: "provider", Message: "invalid provider"},
			expectedStatus: http.StatusBadRequest,
			expectedType:   "invalid_request_error",
		},
		{
			name:           "no available provider",
			mockError:      proxy.ErrNoAvailableProvider,
			expectedStatus: http.StatusServiceUnavailable,
			expectedType:   "service_unavailable",
		},
		{
			name:           "rate limit exceeded",
			mockError:      proxy.ErrRateLimitExceeded,
			expectedStatus: http.StatusTooManyRequests,
			expectedType:   "rate_limit_error",
		},
		{
			name:           "generic error",
			mockError:      errors.New("unknown error"),
			expectedStatus: http.StatusInternalServerError,
			expectedType:   "server_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock service
			mockService := &mockProvidersService{
				err: tt.mockError,
			}

			// Create handler
			handler := NewProvidersHandler(mockService)

			// Create request
			req := httptest.NewRequest("GET", "/api/v1/providers", nil)

			// Create response recorder
			w := httptest.NewRecorder()

			// Handle request
			handler.List(w, req)

			// Check status
			assert.Equal(t, tt.expectedStatus, w.Code)

			// Check error response
			var errResp map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &errResp)
			require.NoError(t, err)

			assert.Contains(t, errResp, "error")
			errorDetails := errResp["error"].(map[string]interface{})
			assert.Equal(t, tt.expectedType, errorDetails["type"])
		})
	}
}
