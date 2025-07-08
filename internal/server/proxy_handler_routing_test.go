package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConnector implements a test connector for proxy handler tests
type testConnector struct {
	name           string
	chatFunc       func(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error)
	chatStreamFunc func(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error)
}

func (t *testConnector) Name() string { return t.name }

func (t *testConnector) Chat(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	if t.chatFunc != nil {
		return t.chatFunc(ctx, req)
	}
	return &connectors.ChatResponse{
		ID:    "test",
		Model: req.Model,
		Choices: []connectors.Choice{
			{Message: connectors.Message{Role: "assistant", Content: "Default response"}},
		},
	}, nil
}

func (t *testConnector) ChatStream(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error) {
	if t.chatStreamFunc != nil {
		return t.chatStreamFunc(ctx, req)
	}
	return nil, errors.New("streaming not implemented")
}

func (t *testConnector) Embeddings(ctx context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
	return nil, errors.New("embeddings not implemented")
}

func (t *testConnector) Models(ctx context.Context) (*connectors.ModelsResponse, error) {
	return &connectors.ModelsResponse{}, nil
}

func (t *testConnector) Health(ctx context.Context) error {
	return nil
}

func (t *testConnector) Close() error {
	return nil
}

func TestProxyHandlerWithRouting(t *testing.T) {
	// Create test registry and handler
	registry := NewConnectorRegistry()
	handler := NewProxyHandler(registry)
	
	// Register mock connectors
	mockOpenAI := &testConnector{
		name: "openai",
		chatFunc: func(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			return &connectors.ChatResponse{
				ID:    "test-1",
				Model: req.Model,
				Choices: []connectors.Choice{
					{Message: connectors.Message{Role: "assistant", Content: "Response from OpenAI"}},
				},
			}, nil
		},
	}
	
	mockAnthropic := &testConnector{
		name: "anthropic",
		chatFunc: func(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			return &connectors.ChatResponse{
				ID:    "test-2",
				Model: req.Model,
				Choices: []connectors.Choice{
					{Message: connectors.Message{Role: "assistant", Content: "Response from Anthropic"}},
				},
			}, nil
		},
	}
	
	registry.Register("openai", mockOpenAI)
	registry.Register("anthropic", mockAnthropic)
	
	t.Run("single model request includes model_used", func(t *testing.T) {
		req := connectors.ChatRequest{
			Model: "openai/gpt-4",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}
		
		body, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		w := httptest.NewRecorder()
		
		handler.handleChatCompletions(w, r)
		
		if w.Code != http.StatusOK {
			t.Logf("Response body: %s", w.Body.String())
		}
		assert.Equal(t, http.StatusOK, w.Code)
		
		var resp map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		
		// Check that model_used field is present
		assert.Equal(t, "openai/gpt-4", resp["model_used"])
		assert.Equal(t, "openai/gpt-4", resp["model"])
	})
	
	t.Run("models array request", func(t *testing.T) {
		req := connectors.ChatRequest{
			Models: []string{"openai/gpt-4", "anthropic/claude-3-sonnet-20240229"},
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}
		
		body, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		w := httptest.NewRecorder()
		
		handler.handleChatCompletions(w, r)
		
		if w.Code != http.StatusOK {
			t.Logf("Response body: %s", w.Body.String())
		}
		assert.Equal(t, http.StatusOK, w.Code)
		
		var resp map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		
		// Should have model_used field
		modelUsed, ok := resp["model_used"].(string)
		assert.True(t, ok)
		assert.Contains(t, []string{"openai/gpt-4", "anthropic/claude-3-sonnet-20240229"}, modelUsed)
	})
	
	t.Run("auto model request", func(t *testing.T) {
		req := connectors.ChatRequest{
			Model: routing.AutoModelID,
			Messages: []connectors.Message{
				{Role: "user", Content: "What is 2+2?"},
			},
		}
		
		body, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		w := httptest.NewRecorder()
		
		handler.handleChatCompletions(w, r)
		
		// Should get a response (auto-routing should work)
		assert.Equal(t, http.StatusOK, w.Code)
		
		var resp map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		
		// Should have selected a model
		assert.NotEmpty(t, resp["model_used"])
	})
	
	t.Run("fallback on error", func(t *testing.T) {
		// Make OpenAI fail
		failingOpenAI := &testConnector{
			name: "openai",
			chatFunc: func(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
				return nil, &connectors.APIError{StatusCode: 429, Message: "Rate limit exceeded"}
			},
		}
		registry.Register("openai", failingOpenAI)
		defer registry.Register("openai", mockOpenAI) // Restore
		
		req := connectors.ChatRequest{
			Models: []string{"openai/gpt-4", "anthropic/claude-3-sonnet-20240229"},
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}
		
		body, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		w := httptest.NewRecorder()
		
		handler.handleChatCompletions(w, r)
		
		if w.Code != http.StatusOK {
			t.Logf("Response body: %s", w.Body.String())
		}
		assert.Equal(t, http.StatusOK, w.Code)
		
		var resp map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		
		// Should have fallen back to anthropic
		assert.Equal(t, "anthropic/claude-3-sonnet-20240229", resp["model_used"])
		
		// Check response content
		choices := resp["choices"].([]interface{})
		message := choices[0].(map[string]interface{})["message"].(map[string]interface{})
		assert.Equal(t, "Response from Anthropic", message["content"])
	})
	
	t.Run("streaming not supported with routing", func(t *testing.T) {
		req := connectors.ChatRequest{
			Models: []string{"openai/gpt-4", "anthropic/claude-3"},
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
			Stream: true,
		}
		
		body, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		w := httptest.NewRecorder()
		
		handler.handleChatCompletions(w, r)
		
		// Should return bad gateway error (from routing failure)
		assert.Equal(t, http.StatusBadGateway, w.Code)
		
		var resp map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		
		errorObj := resp["error"].(map[string]interface{})
		assert.Equal(t, "routing_failed", errorObj["type"])
	})
	
	t.Run("streaming works for single model", func(t *testing.T) {
		// Create streaming mock
		streamingOpenAI := &testConnector{
			name: "openai",
			chatStreamFunc: func(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error) {
				return &routingMockChatStream{
					chunks: []*connectors.ChatStreamChunk{
						{
							ID:    "test-stream",
							Model: req.Model,
							Choices: []connectors.StreamChoice{
								{Delta: connectors.MessageDelta{Content: "Hello "}},
							},
						},
						{
							ID:    "test-stream",
							Model: req.Model,
							Choices: []connectors.StreamChoice{
								{Delta: connectors.MessageDelta{Content: "world!"}},
							},
						},
					},
				}, nil
			},
		}
		registry.Register("openai", streamingOpenAI)
		defer registry.Register("openai", mockOpenAI) // Restore
		
		req := connectors.ChatRequest{
			Model: "openai/gpt-4",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
			Stream: true,
		}
		
		body, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		w := httptest.NewRecorder()
		
		handler.handleChatCompletions(w, r)
		
		if w.Code != http.StatusOK {
			t.Logf("Response body: %s", w.Body.String())
		}
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
		
		// Check that response contains SSE data with model_used in first chunk
		response := w.Body.String()
		assert.Contains(t, response, "data: ")
		assert.Contains(t, response, "model_used")
		assert.Contains(t, response, connectors.SSEDone)
	})
}

func TestRequestMetadataExtraction(t *testing.T) {
	handler := &ProxyHandler{}
	
	t.Run("detect vision content", func(t *testing.T) {
		req := &connectors.ChatRequest{
			Messages: []connectors.Message{
				{
					Role: "user",
					Content: []interface{}{
						map[string]interface{}{"type": "text", "text": "What's this?"},
						map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "test.jpg"}},
					},
				},
			},
		}
		
		metadata := handler.extractRequestMetadata(req)
		assert.Contains(t, metadata.RequiredFeatures, "vision")
	})
	
	t.Run("detect function calling", func(t *testing.T) {
		req := &connectors.ChatRequest{
			Messages: []connectors.Message{
				{Role: "user", Content: "Get weather"},
			},
			Tools: []connectors.Tool{
				{Type: "function", Function: connectors.Function{Name: "get_weather"}},
			},
		}
		
		metadata := handler.extractRequestMetadata(req)
		assert.Contains(t, metadata.RequiredFeatures, "functions")
	})
	
	t.Run("detect streaming", func(t *testing.T) {
		req := &connectors.ChatRequest{
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
			Stream: true,
		}
		
		metadata := handler.extractRequestMetadata(req)
		assert.Contains(t, metadata.RequiredFeatures, "streaming")
	})
}

// routingMockChatStream implements a simple chat stream for testing routing
type routingMockChatStream struct {
	chunks  []*connectors.ChatStreamChunk
	current int
}

func (m *routingMockChatStream) Recv() (*connectors.ChatStreamChunk, error) {
	if m.current >= len(m.chunks) {
		return nil, io.EOF
	}
	chunk := m.chunks[m.current]
	m.current++
	return chunk, nil
}

func (m *routingMockChatStream) Close() error {
	return nil
}