package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/connectors"
	"github.com/agentstation/starport/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachedProxyHandler(t *testing.T) {
	// Create mock storage and cache
	mockStore := storage.NewMockStore()
	cacheConfig := cache.Config{
		MaxSize:     100,
		MaxSizeInMB: 1,
		DefaultTTL:  1 * time.Hour,
	}
	c, err := cache.New(cacheConfig, mockStore)
	require.NoError(t, err)
	defer c.Close()

	// Create mock connector registry
	registry := NewConnectorRegistry()
	mockConfig := connectors.ProviderConfig{
		BaseURL: "https://mock.api",
		Timeout: 30 * time.Second,
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	registry.Register("mock", mockConnector)

	// Create base proxy handler
	baseHandler := NewProxyHandler(registry)

	// Create cached proxy handler
	cachedConfig := CacheConfig{
		EnableChatCache:      true,
		EnableEmbeddingCache: true,
		EnableModelCache:     true,
		EnableProviderCache:  true,
		CacheControlHeader:   "X-Cache-Control",
	}
	cachedHandler := NewCachedProxyHandler(baseHandler, c, cachedConfig)

	// Create router for testing
	r := chi.NewRouter()
	cachedHandler.RegisterRoutes(r)

	t.Run("chat completion caching", func(t *testing.T) {
		// Prepare request
		chatReq := connectors.ChatRequest{
			Model: "mock/test-model",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}
		body, _ := json.Marshal(chatReq)

		// Configure mock response
		mockResponse := &connectors.ChatResponse{
			ID:      "test-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "mock/test-model",
			Choices: []connectors.Choice{
				{
					Index: 0,
					Message: connectors.Message{
						Role:    "assistant",
						Content: "Hello! How can I help you?",
					},
					FinishReason: "stop",
				},
			},
			Usage: connectors.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
			},
		}
		mockConnector.SetChatResponse(mockResponse)

		// First request - should be cache miss
		req1 := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
		req1.Header.Set("Content-Type", "application/json")
		rec1 := httptest.NewRecorder()

		r.ServeHTTP(rec1, req1)

		assert.Equal(t, http.StatusOK, rec1.Code)
		assert.Equal(t, "MISS", rec1.Header().Get("X-Cache"))

		var resp1 connectors.ChatResponse
		err = json.Unmarshal(rec1.Body.Bytes(), &resp1)
		require.NoError(t, err)
		assert.Equal(t, mockResponse.Choices[0].Message.Content, resp1.Choices[0].Message.Content)

		// Second request - should be cache hit
		req2 := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")
		rec2 := httptest.NewRecorder()

		r.ServeHTTP(rec2, req2)

		assert.Equal(t, http.StatusOK, rec2.Code)
		assert.Equal(t, "HIT", rec2.Header().Get("X-Cache"))

		var resp2 connectors.ChatResponse
		err = json.Unmarshal(rec2.Body.Bytes(), &resp2)
		require.NoError(t, err)
		assert.Equal(t, resp1, resp2)

		// Request with cache control header - should bypass cache
		req3 := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
		req3.Header.Set("Content-Type", "application/json")
		req3.Header.Set("X-Cache-Control", "no-cache")
		rec3 := httptest.NewRecorder()

		r.ServeHTTP(rec3, req3)

		assert.Equal(t, http.StatusOK, rec3.Code)
		// No cache header when cache is bypassed
		assert.Empty(t, rec3.Header().Get("X-Cache"))
	})

	t.Run("embedding caching", func(t *testing.T) {
		// Prepare request
		embReq := connectors.EmbeddingsRequest{
			Model: "mock/embedding-model",
			Input: "Test input for embedding",
		}
		body, _ := json.Marshal(embReq)

		// Mock connector returns default embeddings

		// First request - cache miss
		req1 := httptest.NewRequest("POST", "/v1/embeddings", bytes.NewReader(body))
		req1.Header.Set("Content-Type", "application/json")
		rec1 := httptest.NewRecorder()

		r.ServeHTTP(rec1, req1)

		assert.Equal(t, http.StatusOK, rec1.Code)
		assert.Equal(t, "MISS", rec1.Header().Get("X-Cache"))

		// Second request - cache hit
		req2 := httptest.NewRequest("POST", "/v1/embeddings", bytes.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")
		rec2 := httptest.NewRecorder()

		r.ServeHTTP(rec2, req2)

		assert.Equal(t, http.StatusOK, rec2.Code)
		assert.Equal(t, "HIT", rec2.Header().Get("X-Cache"))

		// Verify responses are identical
		assert.Equal(t, rec1.Body.String(), rec2.Body.String())
	})

	t.Run("model list caching", func(t *testing.T) {
		// Mock connector returns default models

		// First request - cache miss
		req1 := httptest.NewRequest("GET", "/v1/models", nil)
		rec1 := httptest.NewRecorder()

		r.ServeHTTP(rec1, req1)

		assert.Equal(t, http.StatusOK, rec1.Code)
		assert.Equal(t, "MISS", rec1.Header().Get("X-Cache"))

		// Second request - cache hit
		req2 := httptest.NewRequest("GET", "/v1/models", nil)
		rec2 := httptest.NewRecorder()

		r.ServeHTTP(rec2, req2)

		assert.Equal(t, http.StatusOK, rec2.Code)
		assert.Equal(t, "HIT", rec2.Header().Get("X-Cache"))
	})

	t.Run("streaming requests bypass cache", func(t *testing.T) {
		// Prepare streaming request
		chatReq := connectors.ChatRequest{
			Model: "mock/test-model",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
			Stream: true,
		}
		body, _ := json.Marshal(chatReq)

		// Request should not be cached
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		// No cache headers for streaming
		assert.Empty(t, rec.Header().Get("X-Cache"))
	})

	t.Run("cache disabled config", func(t *testing.T) {
		// Create handler with cache disabled
		disabledConfig := CacheConfig{
			EnableChatCache: false,
		}
		disabledHandler := NewCachedProxyHandler(baseHandler, c, disabledConfig)

		r2 := chi.NewRouter()
		disabledHandler.RegisterRoutes(r2)

		chatReq := connectors.ChatRequest{
			Model: "mock/test-model",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}
		body, _ := json.Marshal(chatReq)

		// Should not use cache
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		r2.ServeHTTP(rec, req)

		// No cache headers when disabled
		assert.Empty(t, rec.Header().Get("X-Cache"))
	})
}

func TestCacheKeyConsistency(t *testing.T) {
	handler := &CachedProxyHandler{
		keyGen: cache.NewKeyGenerator("test"),
	}

	t.Run("chat request conversion", func(t *testing.T) {
		temp := float32(0.7)
		maxTokens := 100
		
		req := &connectors.ChatRequest{
			Model: "gpt-4",
			Messages: []connectors.Message{
				{Role: "system", Content: "You are helpful"},
				{Role: "user", Content: "Hello"},
			},
			Temperature: &temp,
			MaxTokens:   &maxTokens,
			User:        "test-user",
		}

		cacheReq := handler.toCacheChatRequest(req)

		assert.Equal(t, req.Model, cacheReq.Model)
		assert.Len(t, cacheReq.Messages, 2)
		assert.Equal(t, "system", cacheReq.Messages[0].Role)
		assert.Equal(t, "You are helpful", cacheReq.Messages[0].Content)
		assert.Equal(t, req.Temperature, cacheReq.Temperature)
		assert.Equal(t, req.MaxTokens, cacheReq.MaxTokens)
		assert.Nil(t, cacheReq.N) // ChatRequest doesn't have N field
		assert.Equal(t, "test-user", *cacheReq.User)
	})

	t.Run("embedding request conversion", func(t *testing.T) {
		dims := 1536

		req := &connectors.EmbeddingsRequest{
			Model:          "text-embedding-ada-002",
			Input:          []string{"test", "input"},
			EncodingFormat: "float",
			Dimensions:     &dims,
			User:           "test-user",
		}

		cacheReq := handler.toCacheEmbeddingRequest(req)

		assert.Equal(t, req.Model, cacheReq.Model)
		assert.Equal(t, req.Input, cacheReq.Input)
		assert.Equal(t, "float", *cacheReq.EncodingFormat)
		assert.Equal(t, req.Dimensions, cacheReq.Dimensions)
		assert.Equal(t, "test-user", *cacheReq.User)
	})
}