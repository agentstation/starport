package handlers_test

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

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/handlers"
)

// mockProxy implements proxy.Proxy for testing
type mockProxy struct {
	processChatFunc       func(ctx context.Context, req *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error)
	processChatStreamFunc func(ctx context.Context, req *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error)
}

func (m *mockProxy) ProcessChatCompletion(ctx context.Context, req *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	if m.processChatFunc != nil {
		return m.processChatFunc(ctx, req)
	}
	return &proxy.ChatCompletionResponse{
		ID:     "test-id",
		Object: "chat.completion",
		Model:  req.Model,
	}, nil
}

func (m *mockProxy) ProcessChatCompletionStream(ctx context.Context, req *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
	if m.processChatStreamFunc != nil {
		return m.processChatStreamFunc(ctx, req)
	}
	return nil, errors.New("streaming not implemented in mock")
}

func (m *mockProxy) ProcessEmbeddings(ctx context.Context, req *proxy.EmbeddingsRequest) (*proxy.EmbeddingsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProxy) ListModels(ctx context.Context) (*proxy.ModelsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProxy) ListProviders(ctx context.Context) (*proxy.ProvidersResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProxy) GetModelEndpoints(ctx context.Context, modelID string) (*proxy.ModelEndpointsResponse, error) {
	return nil, errors.New("not implemented")
}

func TestChatHandler_Create(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    interface{}
		mockService    func() *mockProxy
		expectedStatus int
		validateBody   func(t *testing.T, body []byte)
	}{
		{
			name: "successful non-streaming request",
			requestBody: map[string]interface{}{
				"model": "gpt-3.5-turbo",
				"messages": []map[string]string{
					{"role": "user", "content": "Hello"},
				},
			},
			mockService: func() *mockProxy {
				return &mockProxy{
					processChatFunc: func(ctx context.Context, req *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
						return &proxy.ChatCompletionResponse{
							ID:     "test-123",
							Object: "chat.completion",
							Model:  "gpt-3.5-turbo",
						}, nil
					},
				}
			},
			expectedStatus: http.StatusOK,
			validateBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}
				if resp["id"] != "test-123" {
					t.Errorf("expected id test-123, got %v", resp["id"])
				}
			},
		},
		{
			name:           "invalid request body",
			requestBody:    "invalid json",
			mockService:    func() *mockProxy { return &mockProxy{} },
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "validation error",
			requestBody: map[string]interface{}{
				"messages": []map[string]string{
					{"role": "user", "content": "Hello"},
				},
				// missing model
			},
			mockService: func() *mockProxy {
				return &mockProxy{
					processChatFunc: func(ctx context.Context, req *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
						return nil, &proxy.ValidationError{
							Field:   "model",
							Message: "model is required",
						}
					},
				}
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "service error",
			requestBody: map[string]interface{}{
				"model": "gpt-3.5-turbo",
				"messages": []map[string]string{
					{"role": "user", "content": "Hello"},
				},
			},
			mockService: func() *mockProxy {
				return &mockProxy{
					processChatFunc: func(ctx context.Context, req *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
						return nil, errors.New("internal error")
					},
				}
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler with mock service
			handler := handlers.NewChatHandler(tt.mockService())

			// Create request
			body, err := json.Marshal(tt.requestBody)
			if err != nil && tt.requestBody != "invalid json" {
				t.Fatalf("failed to marshal request: %v", err)
			}
			if tt.requestBody == "invalid json" {
				body = []byte("invalid json")
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			// Add context values
			ctx := context.WithValue(req.Context(), "request_id", "test-request-id")
			ctx = context.WithValue(ctx, "api_key", "test-api-key")
			req = req.WithContext(ctx)

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.Create(w, req)

			// Check status
			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			// Validate body if provided
			if tt.validateBody != nil {
				tt.validateBody(t, w.Body.Bytes())
			}
		})
	}
}

// mockStreamResponse implements proxy.ChatCompletionStreamResponse
type mockStreamResponse struct {
	chunks []interface{}
	index  int
}

func (m *mockStreamResponse) Read() (*connectors.ChatStreamChunk, error) {
	if m.index >= len(m.chunks) {
		return nil, io.EOF
	}

	chunk := m.chunks[m.index]
	m.index++

	if err, ok := chunk.(error); ok {
		return nil, err
	}

	return chunk.(*connectors.ChatStreamChunk), nil
}

func (m *mockStreamResponse) Close() error {
	return nil
}

func TestChatHandler_Streaming(t *testing.T) {
	// Create mock service that returns a stream
	mockService := &mockProxy{
		processChatStreamFunc: func(ctx context.Context, req *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
			return &mockStreamResponse{
				chunks: []interface{}{
					&connectors.ChatStreamChunk{
						ID:     "test-stream",
						Object: "chat.completion.chunk",
						Model:  "gpt-3.5-turbo",
					},
				},
			}, nil
		},
	}

	handler := handlers.NewChatHandler(mockService)

	// Create streaming request
	reqBody := map[string]interface{}{
		"model": "gpt-3.5-turbo",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
		"stream": true,
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), "request_id", "test-request-id")
	ctx = context.WithValue(ctx, "api_key", "test-api-key")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	// Call handler
	handler.Create(w, req)

	// Check status
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Check SSE headers
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", ct)
	}

	// Check response contains SSE data
	respBody := w.Body.String()
	if !bytes.Contains([]byte(respBody), []byte("data: ")) {
		t.Error("expected SSE data in response")
	}
	if !bytes.Contains([]byte(respBody), []byte("[DONE]")) {
		t.Error("expected [DONE] marker in response")
	}
}

// TestChatHandler_StreamingCacheHeaders verifies that cache headers are set correctly for streaming responses
func TestChatHandler_StreamingCacheHeaders(t *testing.T) {
	tests := []struct {
		name                string
		setupStream         func() proxy.ChatCompletionStreamResponse
		expectedCacheHeader string
		expectedAgeHeader   string
	}{
		{
			name: "cache hit with age",
			setupStream: func() proxy.ChatCompletionStreamResponse {
				return &mockStreamWithCacheInfo{
					chunks: []connectors.ChatStreamChunk{
						{
							ID:      "test-123",
							Object:  "chat.completion.chunk",
							Created: time.Now().Unix(),
							Model:   "gpt-4",
							Choices: []connectors.StreamChoice{
								{Index: 0, Delta: connectors.MessageDelta{Content: "Hello"}},
							},
						},
					},
					cacheStatus: "HIT",
					cacheAge:    120,
				}
			},
			expectedCacheHeader: "HIT",
			expectedAgeHeader:   "120",
		},
		{
			name: "cache miss no age",
			setupStream: func() proxy.ChatCompletionStreamResponse {
				return &mockStreamWithCacheInfo{
					chunks: []connectors.ChatStreamChunk{
						{
							ID:      "test-123",
							Object:  "chat.completion.chunk",
							Created: time.Now().Unix(),
							Model:   "gpt-4",
							Choices: []connectors.StreamChoice{
								{Index: 0, Delta: connectors.MessageDelta{Content: "Hello"}},
							},
						},
					},
					cacheStatus: "MISS",
					cacheAge:    0,
				}
			},
			expectedCacheHeader: "MISS",
			expectedAgeHeader:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockProxyInstance := &mockProxy{
				processChatStreamFunc: func(ctx context.Context, req *proxy.ChatCompletionRequest) (proxy.ChatCompletionStreamResponse, error) {
					return tt.setupStream(), nil
				},
			}

			handler := handlers.NewChatHandler(mockProxyInstance)

			body := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}],"stream":true}`
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			handler.Create(w, req)

			// Check cache headers
			if got := w.Header().Get("X-Cache"); got != tt.expectedCacheHeader {
				t.Errorf("X-Cache header = %v, want %v", got, tt.expectedCacheHeader)
			}

			if tt.expectedAgeHeader != "" {
				if got := w.Header().Get("X-Cache-Age"); got != tt.expectedAgeHeader {
					t.Errorf("X-Cache-Age header = %v, want %v", got, tt.expectedAgeHeader)
				}
			} else {
				if got := w.Header().Get("X-Cache-Age"); got != "" {
					t.Errorf("X-Cache-Age header = %v, want empty", got)
				}
			}
		})
	}
}

// mockStreamWithCacheInfo implements both ChatCompletionStreamResponse and CacheStatusProvider
type mockStreamWithCacheInfo struct {
	chunks      []connectors.ChatStreamChunk
	position    int
	cacheStatus string
	cacheAge    int
}

func (m *mockStreamWithCacheInfo) Read() (*connectors.ChatStreamChunk, error) {
	if m.position >= len(m.chunks) {
		return nil, io.EOF
	}
	chunk := m.chunks[m.position]
	m.position++
	return &chunk, nil
}

func (m *mockStreamWithCacheInfo) Close() error {
	return nil
}

func (m *mockStreamWithCacheInfo) GetCacheStatus() string {
	return m.cacheStatus
}

func (m *mockStreamWithCacheInfo) GetCacheAge() int {
	return m.cacheAge
}
