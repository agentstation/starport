package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/storage"
)

// mockService implements the Service interface for testing
type mockService struct {
	chatCalls      int
	embeddingCalls int
	modelCalls     int
	providerCalls  int
}

func (m *mockService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	m.chatCalls++
	return &ChatCompletionResponse{
		ID:      "test-response",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []connectors.Choice{
			{
				Index: 0,
				Message: connectors.Message{
					Role:    "assistant",
					Content: "Test response content",
				},
				FinishReason: "stop",
			},
		},
		Usage: &connectors.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}, nil
}

func (m *mockService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	return nil, nil
}

func (m *mockService) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	m.embeddingCalls++
	return &EmbeddingsResponse{
		Model: req.Model,
		Data: []connectors.Embedding{
			{Index: 0, Object: "embedding", Embedding: []float32{0.1, 0.2, 0.3}},
		},
		Usage: &connectors.Usage{
			PromptTokens: 10,
			TotalTokens:  10,
		},
	}, nil
}

func (m *mockService) ListModels(ctx context.Context) (*ModelsResponse, error) {
	m.modelCalls++
	return &ModelsResponse{
		Data: []ModelInfo{
			{ID: "gpt-4", OwnedBy: "openai"},
		},
	}, nil
}

func (m *mockService) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	m.providerCalls++
	return &ProvidersResponse{
		Providers: []ProviderInfo{
			{ID: "openai", Name: "OpenAI"},
		},
	}, nil
}

func (m *mockService) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	return &ModelEndpointsResponse{
		Endpoints: []EndpointInfo{
			{Provider: "openai", Available: true},
		},
	}, nil
}

// TestCacheDisabled verifies that caching can be disabled via configuration
func TestCacheDisabled(t *testing.T) {
	tests := []struct {
		name   string
		config CacheConfig
	}{
		{
			name: "chat cache disabled",
			config: CacheConfig{
				EnableChatCache:      false,
				EnableEmbeddingCache: true,
				EnableModelCache:     true,
				EnableProviderCache:  true,
			},
		},
		{
			name: "embedding cache disabled",
			config: CacheConfig{
				EnableChatCache:      true,
				EnableEmbeddingCache: false,
				EnableModelCache:     true,
				EnableProviderCache:  true,
			},
		},
		{
			name: "all caches disabled",
			config: CacheConfig{
				EnableChatCache:      false,
				EnableEmbeddingCache: false,
				EnableModelCache:     false,
				EnableProviderCache:  false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock service and cache manager
			mockSvc := &mockService{}
			mockStore := storage.NewMockStore()
			cacheManagerConfig := cache.ManagerConfig{
				Responses: struct {
					Strategy      string        `env:"STRATEGY,default=auto"`
					TTL           time.Duration `env:"TTL,default=1h"`
					MaxItemSizeKB int           `env:"MAX_ITEM_SIZE_KB,default=1024"`
					LocalSizeMB   int64         `env:"LOCAL_SIZE_MB,default=256"`
				}{
					Strategy:      "local",
					TTL:           time.Hour,
					MaxItemSizeKB: 1024,
					LocalSizeMB:   256,
				},
			}
			cacheManager, err := cache.NewCacheManager(cacheManagerConfig, mockStore)
			if err != nil {
				t.Fatal(err)
			}
			
			// Create cached service with the test config
			cachedSvc := NewCachedService(mockSvc, cacheManager, tt.config)
			ctx := context.Background()

			// Test chat completion
			if !tt.config.EnableChatCache {
				// Make two identical requests
				req := &ChatCompletionRequest{Model: "gpt-4", Messages: []connectors.Message{{Role: "user", Content: "test"}}}
				_, _ = cachedSvc.ProcessChatCompletion(ctx, req)
				_, _ = cachedSvc.ProcessChatCompletion(ctx, req)
				
				// Should have called the underlying service twice (no caching)
				if mockSvc.chatCalls != 2 {
					t.Errorf("expected 2 chat calls with cache disabled, got %d", mockSvc.chatCalls)
				}
			}

			// Test embeddings
			if !tt.config.EnableEmbeddingCache {
				req := &EmbeddingsRequest{Model: "text-embedding-ada-002", Input: "test"}
				_, _ = cachedSvc.ProcessEmbeddings(ctx, req)
				_, _ = cachedSvc.ProcessEmbeddings(ctx, req)
				
				if mockSvc.embeddingCalls != 2 {
					t.Errorf("expected 2 embedding calls with cache disabled, got %d", mockSvc.embeddingCalls)
				}
			}

			// Test models
			if !tt.config.EnableModelCache {
				_, _ = cachedSvc.ListModels(ctx)
				_, _ = cachedSvc.ListModels(ctx)
				
				if mockSvc.modelCalls != 2 {
					t.Errorf("expected 2 model calls with cache disabled, got %d", mockSvc.modelCalls)
				}
			}

			// Test providers
			if !tt.config.EnableProviderCache {
				_, _ = cachedSvc.ListProviders(ctx)
				_, _ = cachedSvc.ListProviders(ctx)
				
				if mockSvc.providerCalls != 2 {
					t.Errorf("expected 2 provider calls with cache disabled, got %d", mockSvc.providerCalls)
				}
			}
		})
	}
}

// TestCacheEnabled verifies that caching works when enabled
func TestCacheEnabled(t *testing.T) {
	// Create a mock service and cache manager
	mockSvc := &mockService{}
	
	// Use mock store for testing
	mockStore := storage.NewMockStore()
	cacheManagerConfig := cache.ManagerConfig{
		Responses: struct {
			Strategy      string        `env:"STRATEGY,default=auto"`
			TTL           time.Duration `env:"TTL,default=1h"`
			MaxItemSizeKB int           `env:"MAX_ITEM_SIZE_KB,default=1024"`
			LocalSizeMB   int64         `env:"LOCAL_SIZE_MB,default=256"`
		}{
			Strategy:      "local",
			TTL:           time.Hour,
			MaxItemSizeKB: 1024,
			LocalSizeMB:   256,
		},
	}
	cacheManager, err := cache.NewCacheManager(cacheManagerConfig, mockStore)
	if err != nil {
		t.Fatal(err)
	}
	
	// Create cached service with all caches enabled
	config := CacheConfig{
		EnableChatCache:      true,
		EnableEmbeddingCache: true,
		EnableModelCache:     true,
		EnableProviderCache:  true,
	}
	cachedSvc := NewCachedService(mockSvc, cacheManager, config)
	ctx := context.Background()

	// Test chat completion caching
	t.Run("chat completion caching", func(t *testing.T) {
		mockSvc.chatCalls = 0
		req := &ChatCompletionRequest{
			Model:    "gpt-4",
			Messages: []connectors.Message{{Role: "user", Content: "test"}},
		}
		
		// First call should hit the service
		resp1, err := cachedSvc.ProcessChatCompletion(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		
		// Second call should hit the cache
		resp2, err := cachedSvc.ProcessChatCompletion(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		
		// Should have only called the service once
		if mockSvc.chatCalls != 1 {
			t.Errorf("expected 1 chat call with cache enabled, got %d", mockSvc.chatCalls)
		}
		
		// Responses should be identical
		if resp1.ID != resp2.ID {
			t.Error("cached response ID differs from original")
		}
		
		// Verify cache status headers
		if resp1.CacheStatus != "MISS" {
			t.Errorf("expected first response to have cache status MISS, got %s", resp1.CacheStatus)
		}
		if resp2.CacheStatus != "HIT" {
			t.Errorf("expected second response to have cache status HIT, got %s", resp2.CacheStatus)
		}
		
		// IMPORTANT: Verify Choices are preserved through cache (this would catch the type conversion bug)
		if len(resp1.Choices) == 0 {
			t.Fatal("original response has no choices")
		}
		if len(resp2.Choices) == 0 {
			t.Fatal("cached response has no choices - type conversion issue!")
		}
		if len(resp1.Choices) != len(resp2.Choices) {
			t.Errorf("cached response has different number of choices: %d vs %d", len(resp2.Choices), len(resp1.Choices))
		}
		
		// Verify content is preserved
		if resp1.Choices[0].Message.Content != resp2.Choices[0].Message.Content {
			t.Errorf("cached response content differs: %q vs %q", 
				resp2.Choices[0].Message.Content, resp1.Choices[0].Message.Content)
		}
		
		// Verify Usage is preserved
		if resp1.Usage == nil || resp2.Usage == nil {
			t.Error("Usage data missing from response")
		} else if resp1.Usage.TotalTokens != resp2.Usage.TotalTokens {
			t.Errorf("cached response usage differs: %d vs %d tokens", 
				resp2.Usage.TotalTokens, resp1.Usage.TotalTokens)
		}
	})

	// Test embeddings caching
	t.Run("embeddings caching", func(t *testing.T) {
		mockSvc.embeddingCalls = 0
		req := &EmbeddingsRequest{
			Model: "text-embedding-ada-002",
			Input: "test",
		}
		
		_, _ = cachedSvc.ProcessEmbeddings(ctx, req)
		_, _ = cachedSvc.ProcessEmbeddings(ctx, req)
		
		if mockSvc.embeddingCalls != 1 {
			t.Errorf("expected 1 embedding call with cache enabled, got %d", mockSvc.embeddingCalls)
		}
	})
}

// TestCacheTypeConversion verifies that type conversions work correctly
func TestCacheTypeConversion(t *testing.T) {
	// Test that our type conversion helpers handle various data types correctly
	t.Run("choices conversion", func(t *testing.T) {
		// Test with actual connectors.Choice slice
		choices := []connectors.Choice{
			{
				Index: 0,
				Message: connectors.Message{
					Role:    "assistant",
					Content: "test",
				},
			},
		}
		converted := convertToConnectorChoices(choices)
		if len(converted) != 1 {
			t.Errorf("expected 1 choice, got %d", len(converted))
		}
		
		// Test with interface{} containing the data (simulates cache retrieval)
		var interfaceData interface{} = choices
		converted = convertToConnectorChoices(interfaceData)
		if len(converted) != 1 {
			t.Errorf("expected 1 choice from interface{}, got %d", len(converted))
		}
		
		// Test with map data (simulates JSON unmarshaling)
		mapData := []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "test",
				},
			},
		}
		converted = convertToConnectorChoices(mapData)
		if len(converted) != 1 {
			t.Errorf("expected 1 choice from map data, got %d", len(converted))
		}
	})
	
	t.Run("usage conversion", func(t *testing.T) {
		// Test with actual Usage pointer
		usage := &connectors.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		}
		converted := convertToConnectorUsage(usage)
		if converted == nil || converted.TotalTokens != 15 {
			t.Error("failed to convert Usage pointer")
		}
		
		// Test with non-pointer Usage
		nonPointerUsage := connectors.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		}
		converted = convertToConnectorUsage(nonPointerUsage)
		if converted == nil || converted.TotalTokens != 15 {
			t.Error("failed to convert non-pointer Usage")
		}
		
		// Test with interface{} containing the data
		var interfaceData interface{} = usage
		converted = convertToConnectorUsage(interfaceData)
		if converted == nil || converted.TotalTokens != 15 {
			t.Error("failed to convert Usage from interface{}")
		}
	})
}

// TestCacheSkipModels verifies that specific models can be excluded from caching
func TestCacheSkipModels(t *testing.T) {
	mockSvc := &mockService{}
	mockStore := storage.NewMockStore()
	cacheManagerConfig := cache.ManagerConfig{
		Responses: struct {
			Strategy      string        `env:"STRATEGY,default=auto"`
			TTL           time.Duration `env:"TTL,default=1h"`
			MaxItemSizeKB int           `env:"MAX_ITEM_SIZE_KB,default=1024"`
			LocalSizeMB   int64         `env:"LOCAL_SIZE_MB,default=256"`
		}{
			Strategy:      "local",
			TTL:           time.Hour,
			MaxItemSizeKB: 1024,
			LocalSizeMB:   256,
		},
	}
	cacheManager, err := cache.NewCacheManager(cacheManagerConfig, mockStore)
	if err != nil {
		t.Fatal(err)
	}
	
	config := CacheConfig{
		EnableChatCache: true,
		SkipCacheModels: []string{"gpt-4-turbo", "claude"},
	}
	cachedSvc := NewCachedService(mockSvc, cacheManager, config)
	ctx := context.Background()

	tests := []struct {
		model       string
		shoudCache bool
	}{
		{"gpt-4", true},
		{"gpt-4-turbo", false},
		{"claude-3-opus", false},
		{"gpt-3.5-turbo", true},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			mockSvc.chatCalls = 0
			req := &ChatCompletionRequest{
				Model:    tt.model,
				Messages: []connectors.Message{{Role: "user", Content: "test"}},
			}
			
			// Make two identical requests
			_, _ = cachedSvc.ProcessChatCompletion(ctx, req)
			_, _ = cachedSvc.ProcessChatCompletion(ctx, req)
			
			expectedCalls := 1
			if !tt.shoudCache {
				expectedCalls = 2
			}
			
			if mockSvc.chatCalls != expectedCalls {
				t.Errorf("model %s: expected %d calls, got %d", tt.model, expectedCalls, mockSvc.chatCalls)
			}
		})
	}
}

// TestCacheControlHeader verifies that cache can be bypassed using headers
func TestCacheControlHeader(t *testing.T) {
	mockSvc := &mockService{}
	mockStore := storage.NewMockStore()
	cacheManagerConfig := cache.ManagerConfig{
		Responses: struct {
			Strategy      string        `env:"STRATEGY,default=auto"`
			TTL           time.Duration `env:"TTL,default=1h"`
			MaxItemSizeKB int           `env:"MAX_ITEM_SIZE_KB,default=1024"`
			LocalSizeMB   int64         `env:"LOCAL_SIZE_MB,default=256"`
		}{
			Strategy:      "local",
			TTL:           time.Hour,
			MaxItemSizeKB: 1024,
			LocalSizeMB:   256,
		},
	}
	cacheManager, err := cache.NewCacheManager(cacheManagerConfig, mockStore)
	if err != nil {
		t.Fatal(err)
	}
	
	config := CacheConfig{
		EnableChatCache:    true,
		CacheControlHeader: "X-Cache-Control",
	}
	cachedSvc := NewCachedService(mockSvc, cacheManager, config)

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []connectors.Message{{Role: "user", Content: "test"}},
	}

	// First request to populate cache
	ctx := context.Background()
	_, _ = cachedSvc.ProcessChatCompletion(ctx, req)

	// Second request with no-cache header
	mockSvc.chatCalls = 0
	ctxNoCache := context.WithValue(ctx, "X-Cache-Control", "no-cache")
	_, _ = cachedSvc.ProcessChatCompletion(ctxNoCache, req)

	if mockSvc.chatCalls != 1 {
		t.Errorf("expected cache to be bypassed with no-cache header, got %d calls", mockSvc.chatCalls)
	}
}
