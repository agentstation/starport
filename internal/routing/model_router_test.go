package routing

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/agentstation/starport/internal/connectors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConnector implements a test connector
type mockConnector struct {
	name           string
	chatFunc       func(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error)
	chatStreamFunc func(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error)
	shouldFail     bool
	failureType    FallbackTrigger
}

func (m *mockConnector) Name() string { return m.name }

func (m *mockConnector) Chat(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	if m.chatFunc != nil {
		return m.chatFunc(ctx, req)
	}
	
	if m.shouldFail {
		switch m.failureType {
		case FallbackRateLimit:
			return nil, &connectors.APIError{StatusCode: 429, Message: "Rate limit exceeded"}
		case FallbackModelUnavailable:
			return nil, &connectors.APIError{StatusCode: 404, Message: "Model not found"}
		case FallbackContextExceeded:
			return nil, &connectors.APIError{StatusCode: 400, Message: "context_length_exceeded"}
		case FallbackProviderError:
			return nil, &connectors.APIError{StatusCode: 500, Message: "Internal server error"}
		default:
			return nil, errors.New("unknown error")
		}
	}
	
	return &connectors.ChatResponse{
		ID:    "test-response",
		Model: req.Model,
		Choices: []connectors.Choice{
			{Message: connectors.Message{Role: "assistant", Content: "Test response"}},
		},
	}, nil
}

func (m *mockConnector) ChatStream(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error) {
	if m.chatStreamFunc != nil {
		return m.chatStreamFunc(ctx, req)
	}
	return nil, errors.New("streaming not implemented")
}

func (m *mockConnector) Embeddings(ctx context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
	return nil, errors.New("embeddings not implemented")
}

func (m *mockConnector) Models(ctx context.Context) (*connectors.ModelsResponse, error) {
	return &connectors.ModelsResponse{}, nil
}

func (m *mockConnector) Health(ctx context.Context) error {
	if m.shouldFail {
		return errors.New("unhealthy")
	}
	return nil
}

func (m *mockConnector) Close() error {
	return nil
}

// mockRegistry implements a test registry
type mockRegistry struct {
	connectors map[string]connectors.Connector
}

func (r *mockRegistry) Get(provider string) connectors.Connector {
	return r.connectors[provider]
}

func (r *mockRegistry) List() []string {
	providers := make([]string, 0, len(r.connectors))
	for p := range r.connectors {
		providers = append(providers, p)
	}
	return providers
}

func TestIsFallbackError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantTrigger  FallbackTrigger
		wantFallback bool
	}{
		{
			name:         "nil error",
			err:          nil,
			wantTrigger:  FallbackNone,
			wantFallback: false,
		},
		{
			name:         "rate limit error",
			err:          &connectors.APIError{StatusCode: 429, Message: "Rate limit exceeded"},
			wantTrigger:  FallbackRateLimit,
			wantFallback: true,
		},
		{
			name:         "model not found",
			err:          &connectors.APIError{StatusCode: 404, Message: "Model not found"},
			wantTrigger:  FallbackModelUnavailable,
			wantFallback: true,
		},
		{
			name:         "context length exceeded",
			err:          &connectors.APIError{StatusCode: 400, Message: "context_length_exceeded"},
			wantTrigger:  FallbackContextExceeded,
			wantFallback: true,
		},
		{
			name:         "content policy violation",
			err:          &connectors.APIError{StatusCode: 400, Message: "content_policy_violation"},
			wantTrigger:  FallbackContentModeration,
			wantFallback: true,
		},
		{
			name:         "server error",
			err:          &connectors.APIError{StatusCode: 500, Message: "Internal server error"},
			wantTrigger:  FallbackProviderError,
			wantFallback: true,
		},
		{
			name:         "timeout error",
			err:          errors.New("request timeout"),
			wantTrigger:  FallbackTimeout,
			wantFallback: true,
		},
		{
			name:         "non-fallback error",
			err:          &connectors.APIError{StatusCode: 401, Message: "Unauthorized"},
			wantTrigger:  FallbackNone,
			wantFallback: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger, shouldFallback := IsFallbackError(tt.err)
			assert.Equal(t, tt.wantTrigger, trigger)
			assert.Equal(t, tt.wantFallback, shouldFallback)
		})
	}
}

func TestRouteWithFallback(t *testing.T) {
	ctx := context.Background()
	
	// Create mock connectors
	openaiConnector := &mockConnector{name: "openai"}
	anthropicConnector := &mockConnector{name: "anthropic"}
	groqConnector := &mockConnector{name: "groq"}
	
	registry := &mockRegistry{
		connectors: map[string]connectors.Connector{
			"openai":    openaiConnector,
			"anthropic": anthropicConnector,
			"groq":      groqConnector,
		},
	}
	
	router := NewRouter(registry)

	t.Run("single model success", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Model: "openai/gpt-4",
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "openai/gpt-4", resp.ModelUsed)
		assert.Equal(t, "openai", resp.ProviderUsed)
		assert.Equal(t, 1, resp.Attempts)
	})

	t.Run("fallback on rate limit", func(t *testing.T) {
		// Make openai fail with rate limit
		openaiConnector.shouldFail = true
		openaiConnector.failureType = FallbackRateLimit
		defer func() { openaiConnector.shouldFail = false }()
		
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{"openai/gpt-4", "anthropic/claude-3-sonnet-20240229"},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "anthropic/claude-3-sonnet-20240229", resp.ModelUsed)
		assert.Equal(t, "anthropic", resp.ProviderUsed)
		assert.Equal(t, 2, resp.Attempts)
		assert.Len(t, resp.Metadata.ModelsAttempted, 2)
		assert.Equal(t, "failed", resp.Metadata.ModelsAttempted[0].Status)
		assert.Equal(t, "success", resp.Metadata.ModelsAttempted[1].Status)
	})

	t.Run("all models fail", func(t *testing.T) {
		// Make all connectors fail
		openaiConnector.shouldFail = true
		anthropicConnector.shouldFail = true
		defer func() {
			openaiConnector.shouldFail = false
			anthropicConnector.shouldFail = false
		}()
		
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{"openai/gpt-4", "anthropic/claude-3-sonnet-20240229"},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "all models failed")
		if resp != nil && resp.Metadata != nil {
			assert.Len(t, resp.Metadata.ModelsAttempted, 2)
			assert.Equal(t, "failed", resp.Metadata.ModelsAttempted[0].Status)
			assert.Equal(t, "failed", resp.Metadata.ModelsAttempted[1].Status)
		}
	})

	t.Run("provider preferences", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{
					"openai/gpt-4",
					"anthropic/claude-3-sonnet-20240229", 
					"groq/llama-3.1-8b-instant",
				},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			ProviderPreferences: &ProviderPreferences{
				Order: []string{"groq", "anthropic", "openai"},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		// Should use groq first due to preference order
		assert.Equal(t, "groq/llama-3.1-8b-instant", resp.ModelUsed)
		assert.Equal(t, "groq", resp.ProviderUsed)
	})

	t.Run("provider only filter", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{
					"openai/gpt-4",
					"anthropic/claude-3-sonnet-20240229",
					"groq/llama-3.1-8b-instant",
				},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			ProviderPreferences: &ProviderPreferences{
				Only: []string{"anthropic"},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		// Should only use anthropic
		assert.Equal(t, "anthropic/claude-3-sonnet-20240229", resp.ModelUsed)
		assert.Equal(t, "anthropic", resp.ProviderUsed)
	})

	t.Run("provider ignore filter", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{
					"openai/gpt-4",
					"anthropic/claude-3-sonnet-20240229",
				},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			ProviderPreferences: &ProviderPreferences{
				Ignore: []string{"openai"},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		// Should skip openai and use anthropic
		assert.Equal(t, "anthropic/claude-3-sonnet-20240229", resp.ModelUsed)
		assert.Equal(t, "anthropic", resp.ProviderUsed)
	})

	t.Run("auto model selection", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Model: AutoModelID,
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		// Should select a model automatically
		assert.NotEmpty(t, resp.ModelUsed)
		assert.NotEmpty(t, resp.ProviderUsed)
	})

	t.Run("circuit breaker", func(t *testing.T) {
		// Create a new router for this test
		testRouter := &defaultRouter{
			registry:        registry,
			modelSelector:   NewDefaultModelSelector(),
			availableModels: make(map[string]ModelInfo),
			providerHealth:  make(map[string]*ProviderHealth),
		}
		
		// Record multiple failures to open circuit
		for i := 0; i < 3; i++ {
			testRouter.recordProviderFailure("openai", errors.New("test error"))
		}
		
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{"openai/gpt-4", "anthropic/claude-3-sonnet-20240229"},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
		}
		
		resp, err := testRouter.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		// Should skip openai due to circuit breaker
		assert.Equal(t, "anthropic/claude-3-sonnet-20240229", resp.ModelUsed)
		// Should have 2 attempts: 1 skipped (openai), 1 success (anthropic)
		assert.Equal(t, 2, len(resp.Metadata.ModelsAttempted))
		assert.Equal(t, "skipped", resp.Metadata.ModelsAttempted[0].Status)
		assert.Equal(t, "provider circuit open", resp.Metadata.ModelsAttempted[0].Error)
		assert.Equal(t, "success", resp.Metadata.ModelsAttempted[1].Status)
	})
}

func TestModelSelection(t *testing.T) {
	selector := NewDefaultModelSelector()
	
	t.Run("simple text request", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{Role: "user", Content: "What is 2+2?"},
				},
			},
		}
		
		models := selector.SelectModels(req)
		assert.NotEmpty(t, models)
		// Should start with fast, economical models
		assert.Contains(t, models[0], "groq/llama-3.1-8b-instant")
	})
	
	t.Run("vision request", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{
						Role: "user",
						Content: []interface{}{
							map[string]interface{}{"type": "text", "text": "What's in this image?"},
							map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/jpeg;base64,..."}},
						},
					},
				},
			},
		}
		
		models := selector.SelectModels(req)
		assert.NotEmpty(t, models)
		// Should include vision-capable models
		for _, model := range models[:3] {
			cap, _ := GetModelCapability(model)
			assert.True(t, cap.SupportsVision, fmt.Sprintf("Model %s should support vision", model))
		}
	})
	
	t.Run("function calling request", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{Role: "user", Content: "Get the weather in NYC"},
				},
				Tools: []connectors.Tool{
					{Type: "function", Function: connectors.Function{Name: "get_weather"}},
				},
			},
		}
		
		models := selector.SelectModels(req)
		assert.NotEmpty(t, models)
		// Should include function-capable models
		for _, model := range models {
			if contains(model, "openai") || contains(model, "mistral") {
				cap, _ := GetModelCapability(model)
				assert.True(t, cap.SupportsFunctions, fmt.Sprintf("Model %s should support functions", model))
			}
		}
	})
	
	t.Run("quality preference", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			Metadata: &RequestMetadata{
				UserPreferences: map[string]interface{}{
					"quality": "premium",
				},
			},
		}
		
		models := selector.SelectModels(req)
		assert.NotEmpty(t, models)
		// Check that premium models are prioritized
		foundPremium := false
		for _, model := range models {
			cap, ok := GetModelCapability(model)
			if ok && cap.Quality == "premium" {
				foundPremium = true
				break
			}
		}
		assert.True(t, foundPremium, "Should include premium models")
	})
}

func TestAPIKeyRestrictions(t *testing.T) {
	registry := &mockRegistry{
		connectors: map[string]connectors.Connector{
			"openai":    &mockConnector{name: "openai"},
			"anthropic": &mockConnector{name: "anthropic"},
			"groq":      &mockConnector{name: "groq"},
		},
	}
	
	router := NewRouter(registry)
	ctx := context.Background()
	
	t.Run("allowed providers only", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{
					"openai/gpt-4",
					"anthropic/claude-3-sonnet-20240229",
					"groq/llama-3.1-8b-instant",
				},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			APIKeyConfig: &APIKeyConfig{
				AllowedProviders: []string{"anthropic", "groq"},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		// Should only use allowed providers
		assert.Contains(t, []string{"anthropic", "groq"}, resp.ProviderUsed)
	})
	
	t.Run("model override", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Model: "openai/gpt-4",
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			APIKeyConfig: &APIKeyConfig{
				AllowedProviders: []string{"openai"},
				ModelOverrides: map[string]string{
					"openai/gpt-4": "openai/gpt-3.5-turbo",
				},
			},
		}
		
		resp, err := router.RouteWithFallback(ctx, req)
		require.NoError(t, err)
		// Should use the override model
		assert.Equal(t, "openai/gpt-3.5-turbo", resp.ModelUsed)
	})
}