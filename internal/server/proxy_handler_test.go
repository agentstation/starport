package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/connectors"
)

// mockConnector implements the Connector interface for testing
type mockConnector struct {
	name string
	// Response mocks
	chatResponse       *connectors.ChatResponse
	chatError          error
	streamChunks       []*connectors.ChatStreamChunk
	streamError        error
	embeddingsResponse *connectors.EmbeddingsResponse
	embeddingsError    error
	modelsResponse     *connectors.ModelsResponse
	modelsError        error
}

func (m *mockConnector) Chat(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	if m.chatError != nil {
		return nil, m.chatError
	}
	if m.chatResponse != nil {
		return m.chatResponse, nil
	}
	// Default response
	return &connectors.ChatResponse{
		ID:      "test-id",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []connectors.Choice{
			{
				Index: 0,
				Message: connectors.Message{
					Role:    connectors.RoleAssistant,
					Content: "Test response",
				},
				FinishReason: "stop",
			},
		},
		Usage: connectors.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}, nil
}

func (m *mockConnector) ChatStream(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error) {
	if m.streamError != nil {
		return nil, m.streamError
	}
	return &mockChatStream{
		chunks: m.streamChunks,
		index:  0,
	}, nil
}

func (m *mockConnector) Embeddings(ctx context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
	if m.embeddingsError != nil {
		return nil, m.embeddingsError
	}
	if m.embeddingsResponse != nil {
		return m.embeddingsResponse, nil
	}
	// Default response
	return &connectors.EmbeddingsResponse{
		Object: "list",
		Data: []connectors.Embedding{
			{
				Object:    "embedding",
				Index:     0,
				Embedding: []float32{0.1, 0.2, 0.3},
			},
		},
		Model: req.Model,
		Usage: connectors.Usage{
			PromptTokens: 5,
			TotalTokens:  5,
		},
	}, nil
}

func (m *mockConnector) Models(ctx context.Context) (*connectors.ModelsResponse, error) {
	if m.modelsError != nil {
		return nil, m.modelsError
	}
	if m.modelsResponse != nil {
		return m.modelsResponse, nil
	}
	// Default response
	return &connectors.ModelsResponse{
		Object: "list",
		Data: []connectors.Model{
			{
				ID:      "test-model",
				Object:  "model",
				Created: time.Now().Unix(),
				OwnedBy: m.name,
			},
		},
	}, nil
}

func (m *mockConnector) Health(ctx context.Context) error {
	return nil
}

func (m *mockConnector) Name() string {
	return m.name
}

func (m *mockConnector) Close() error {
	return nil
}

// mockChatStream implements ChatStream interface
type mockChatStream struct {
	chunks []*connectors.ChatStreamChunk
	index  int
}

func (s *mockChatStream) Recv() (*connectors.ChatStreamChunk, error) {
	if s.index >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *mockChatStream) Close() error {
	return nil
}

func TestProxyHandler_ChatCompletions(t *testing.T) {
	tests := []struct {
		name           string
		request        interface{}
		mockResponse   *connectors.ChatResponse
		mockError      error
		expectedStatus int
		expectedError  string
	}{
		{
			name: "successful chat completion",
			request: connectors.ChatRequest{
				Model: "mock/test-model",
				Messages: []connectors.Message{
					{
						Role:    connectors.RoleUser,
						Content: "Hello",
					},
				},
			},
			mockResponse: &connectors.ChatResponse{
				ID:      "test-123",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "test-model",
				Choices: []connectors.Choice{
					{
						Index: 0,
						Message: connectors.Message{
							Role:    connectors.RoleAssistant,
							Content: "Hello! How can I help you?",
						},
						FinishReason: "stop",
					},
				},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "invalid request - missing model",
			request: connectors.ChatRequest{
				Messages: []connectors.Message{
					{
						Role:    connectors.RoleUser,
						Content: "Hello",
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "model or models array is required",
		},
		{
			name: "invalid request - missing messages",
			request: connectors.ChatRequest{
				Model: "mock/test-model",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "messages are required",
		},
		{
			name: "invalid model format",
			request: connectors.ChatRequest{
				Model: "invalid-model",
				Messages: []connectors.Message{
					{
						Role:    connectors.RoleUser,
						Content: "Hello",
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Model not found",
		},
		{
			name: "provider not found",
			request: connectors.ChatRequest{
				Model: "unknown/test-model",
				Messages: []connectors.Message{
					{
						Role:    connectors.RoleUser,
						Content: "Hello",
					},
				},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Model not found",
		},
		{
			name: "connector error",
			request: connectors.ChatRequest{
				Model: "mock/test-model",
				Messages: []connectors.Message{
					{
						Role:    connectors.RoleUser,
						Content: "Hello",
					},
				},
			},
			mockError: &connectors.APIError{
				StatusCode: http.StatusTooManyRequests,
				Type:       "rate_limit_error",
				Message:    "Rate limit exceeded",
			},
			expectedStatus: http.StatusTooManyRequests,
			expectedError:  "Rate limit exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			registry := NewConnectorRegistry()
			mockConn := &mockConnector{
				name:         "mock",
				chatResponse: tt.mockResponse,
				chatError:    tt.mockError,
			}
			registry.Register("mock", mockConn)

			handler := NewProxyHandler(registry)
			router := chi.NewRouter()
			handler.RegisterRoutes(router)
			router.Route("/api/v1", func(r chi.Router) {
				handler.RegisterOpenRouterRoutes(r)
			})

			// Create request
			body, err := json.Marshal(tt.request)
			require.NoError(t, err)

			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			// Execute
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Assert
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedError != "" {
				var errResp map[string]interface{}
				err = json.Unmarshal(w.Body.Bytes(), &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["error"].(map[string]interface{})["message"], tt.expectedError)
			} else {
				var resp connectors.ChatResponse
				err = json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, "mock/test-model", resp.Model) // Should include provider prefix
			}
		})
	}
}

func TestProxyHandler_ChatCompletionsStreaming(t *testing.T) {
	// Setup
	registry := NewConnectorRegistry()
	
	chunks := []*connectors.ChatStreamChunk{
		{
			ID:      "test-123",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   "test-model",
			Choices: []connectors.StreamChoice{
				{
					Index: 0,
					Delta: connectors.MessageDelta{
						Role: connectors.RoleAssistant,
					},
				},
			},
		},
		{
			ID:      "test-123",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   "test-model",
			Choices: []connectors.StreamChoice{
				{
					Index: 0,
					Delta: connectors.MessageDelta{
						Content: "Hello!",
					},
				},
			},
		},
		{
			ID:      "test-123",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   "test-model",
			Choices: []connectors.StreamChoice{
				{
					Index:        0,
					Delta:        connectors.MessageDelta{},
					FinishReason: "stop",
				},
			},
		},
	}

	mockConn := &mockConnector{
		name:         "mock",
		streamChunks: chunks,
	}
	registry.Register("mock", mockConn)

	handler := NewProxyHandler(registry)
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	router.Route("/api/v1", func(r chi.Router) {
		handler.RegisterOpenRouterRoutes(r)
	})

	// Create streaming request
	reqBody := connectors.ChatRequest{
		Model: "mock/test-model",
		Messages: []connectors.Message{
			{
				Role:    connectors.RoleUser,
				Content: "Hello",
			},
		},
		Stream: true,
	}

	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Execute
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))

	// Check SSE format
	body = w.Body.Bytes()
	lines := strings.Split(string(body), "\n")
	
	// Should have data lines and [DONE] marker
	dataLines := 0
	hasDone := false
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				hasDone = true
			} else if data != "" {
				// Verify it's valid JSON
				var chunk connectors.ChatStreamChunk
				err := json.Unmarshal([]byte(data), &chunk)
				assert.NoError(t, err)
				assert.Equal(t, "mock/test-model", chunk.Model)
				dataLines++
			}
		}
	}

	assert.Equal(t, len(chunks), dataLines)
	assert.True(t, hasDone)
}

func TestProxyHandler_Embeddings(t *testing.T) {
	tests := []struct {
		name           string
		request        interface{}
		mockResponse   *connectors.EmbeddingsResponse
		mockError      error
		expectedStatus int
		expectedError  string
	}{
		{
			name: "successful embeddings",
			request: connectors.EmbeddingsRequest{
				Model: "mock/embedding-model",
				Input: "Test input",
			},
			mockResponse: &connectors.EmbeddingsResponse{
				Object: "list",
				Data: []connectors.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: []float32{0.1, 0.2, 0.3, 0.4, 0.5},
					},
				},
				Model: "embedding-model",
				Usage: connectors.Usage{
					PromptTokens: 3,
					TotalTokens:  3,
				},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "invalid request - missing model",
			request: connectors.EmbeddingsRequest{
				Input: "Test input",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "model is required",
		},
		{
			name: "invalid request - missing input",
			request: connectors.EmbeddingsRequest{
				Model: "mock/embedding-model",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "input is required",
		},
		{
			name: "invalid encoding format",
			request: connectors.EmbeddingsRequest{
				Model:          "mock/embedding-model",
				Input:          "Test input",
				EncodingFormat: "invalid",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "encoding_format must be 'float' or 'base64'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			registry := NewConnectorRegistry()
			mockConn := &mockConnector{
				name:               "mock",
				embeddingsResponse: tt.mockResponse,
				embeddingsError:    tt.mockError,
			}
			registry.Register("mock", mockConn)

			handler := NewProxyHandler(registry)
			router := chi.NewRouter()
			handler.RegisterRoutes(router)
			router.Route("/api/v1", func(r chi.Router) {
				handler.RegisterOpenRouterRoutes(r)
			})

			// Create request
			body, err := json.Marshal(tt.request)
			require.NoError(t, err)

			req := httptest.NewRequest("POST", "/v1/embeddings", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			// Execute
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Assert
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedError != "" {
				var errResp map[string]interface{}
				err = json.Unmarshal(w.Body.Bytes(), &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["error"].(map[string]interface{})["message"], tt.expectedError)
			} else {
				var resp connectors.EmbeddingsResponse
				err = json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, "mock/embedding-model", resp.Model)
			}
		})
	}
}

func TestProxyHandler_Models(t *testing.T) {
	// Setup
	registry := NewConnectorRegistry()
	
	// Register multiple mock connectors
	mockConn1 := &mockConnector{
		name: "provider1",
		modelsResponse: &connectors.ModelsResponse{
			Object: "list",
			Data: []connectors.Model{
				{
					ID:      "model1",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "provider1",
				},
				{
					ID:      "model2",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "provider1",
				},
			},
		},
	}
	registry.Register("provider1", mockConn1)

	mockConn2 := &mockConnector{
		name: "provider2",
		modelsResponse: &connectors.ModelsResponse{
			Object: "list",
			Data: []connectors.Model{
				{
					ID:      "model3",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "provider2",
				},
			},
		},
	}
	registry.Register("provider2", mockConn2)

	// Test error handling
	mockConn3 := &mockConnector{
		name:        "provider3",
		modelsError: errors.New("provider unavailable"),
	}
	registry.Register("provider3", mockConn3)

	handler := NewProxyHandler(registry)
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	router.Route("/api/v1", func(r chi.Router) {
		handler.RegisterOpenRouterRoutes(r)
	})

	// Test both endpoints
	endpoints := []string{"/v1/models", "/api/v1/models"}
	
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			req := httptest.NewRequest("GET", endpoint, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Assert
			assert.Equal(t, http.StatusOK, w.Code)

			var resp connectors.ModelsResponse
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err)

			// Should have models from provider1 and provider2, but not provider3 (error)
			assert.Equal(t, "list", resp.Object)
			assert.Len(t, resp.Data, 3)

			// Check that all models have provider prefix
			modelIDs := make(map[string]bool)
			for _, model := range resp.Data {
				modelIDs[model.ID] = true
			}

			assert.True(t, modelIDs["provider1/model1"])
			assert.True(t, modelIDs["provider1/model2"])
			assert.True(t, modelIDs["provider2/model3"])
		})
	}
}

func TestProxyHandler_Providers(t *testing.T) {
	// Setup
	registry := NewConnectorRegistry()
	handler := NewProxyHandler(registry)
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	router.Route("/api/v1", func(r chi.Router) {
		handler.RegisterOpenRouterRoutes(r)
	})

	// Test
	req := httptest.NewRequest("GET", "/api/v1/providers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	data, ok := resp["data"].([]interface{})
	require.True(t, ok)
	assert.Greater(t, len(data), 0)

	// Check first provider has required fields
	provider := data[0].(map[string]interface{})
	assert.Contains(t, provider, "name")
	assert.Contains(t, provider, "slug")
	assert.Contains(t, provider, "logging_policy")
	assert.Contains(t, provider, "privacy_policy_url")
	assert.Contains(t, provider, "is_moderated")
}

func TestProxyHandler_OpenRouterCompatibility(t *testing.T) {
	// Setup
	registry := NewConnectorRegistry()
	mockConn := &mockConnector{name: "mock"}
	registry.Register("mock", mockConn)

	handler := NewProxyHandler(registry)
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	router.Route("/api/v1", func(r chi.Router) {
		handler.RegisterOpenRouterRoutes(r)
	})

	// Test that both OpenAI and OpenRouter style endpoints work
	endpoints := map[string]string{
		"chat":       "/v1/chat/completions",
		"chat_or":    "/api/v1/chat/completions",
		"embeddings": "/v1/embeddings",
		"embed_or":   "/api/v1/embeddings",
		"models":     "/v1/models",
		"models_or":  "/api/v1/models",
	}

	for name, endpoint := range endpoints {
		t.Run(name, func(t *testing.T) {
			var req *http.Request
			if strings.Contains(name, "models") {
				req = httptest.NewRequest("GET", endpoint, nil)
			} else {
				// Create minimal valid request
				var body []byte
				if strings.Contains(name, "chat") {
					chatReq := connectors.ChatRequest{
						Model: "mock/test",
						Messages: []connectors.Message{
							{Role: connectors.RoleUser, Content: "test"},
						},
					}
					body, _ = json.Marshal(chatReq)
				} else {
					embedReq := connectors.EmbeddingsRequest{
						Model: "mock/test",
						Input: "test",
					}
					body, _ = json.Marshal(embedReq)
				}
				req = httptest.NewRequest("POST", endpoint, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
			}

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Should not return 404
			assert.NotEqual(t, http.StatusNotFound, w.Code)
		})
	}
}