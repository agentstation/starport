package proxy

import (
	"context"
	"testing"

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
		ID:    "test-response",
		Model: req.Model,
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
			// Create a mock service and cache
			mockSvc := &mockService{}
			mockStore := storage.NewMockStore()
			cacheConfig := cache.Config{
				MaxSize:     1000,
				MaxSizeInMB: 10,
			}
			mockCache, err := cache.New(cacheConfig, mockStore)
			if err != nil {
				t.Fatal(err)
			}
			
			// Create cached service with the test config
			cachedSvc := NewCachedService(mockSvc, mockCache, tt.config)
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
	// Create a mock service and cache
	mockSvc := &mockService{}
	
	// Use mock store for testing
	mockStore := storage.NewMockStore()
	cacheConfig := cache.Config{
		MaxSize:     1000,
		MaxSizeInMB: 10,
	}
	mockCache, err := cache.New(cacheConfig, mockStore)
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
	cachedSvc := NewCachedService(mockSvc, mockCache, config)
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
			t.Error("cached response differs from original")
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

// TestCacheSkipModels verifies that specific models can be excluded from caching
func TestCacheSkipModels(t *testing.T) {
	mockSvc := &mockService{}
	mockStore := storage.NewMockStore()
	cacheConfig := cache.Config{
		MaxSize:     1000,
		MaxSizeInMB: 10,
	}
	mockCache, err := cache.New(cacheConfig, mockStore)
	if err != nil {
		t.Fatal(err)
	}
	
	config := CacheConfig{
		EnableChatCache: true,
		SkipCacheModels: []string{"gpt-4-turbo", "claude"},
	}
	cachedSvc := NewCachedService(mockSvc, mockCache, config)
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
	cacheConfig := cache.Config{
		MaxSize:     1000,
		MaxSizeInMB: 10,
	}
	mockCache, err := cache.New(cacheConfig, mockStore)
	if err != nil {
		t.Fatal(err)
	}
	
	config := CacheConfig{
		EnableChatCache:    true,
		CacheControlHeader: "X-Cache-Control",
	}
	cachedSvc := NewCachedService(mockSvc, mockCache, config)

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
