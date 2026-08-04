package router

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConnector implements a test connector
type mockConnector struct {
	name           string
	chatFunc       func(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error)
	chatStreamFunc func(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error)
	shouldFail     bool
	failureStatus  int
}

func (m *mockConnector) Name() string { return m.name }

func (m *mockConnector) Chat(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	if m.chatFunc != nil {
		return m.chatFunc(ctx, req)
	}

	if m.shouldFail {
		switch m.failureStatus {
		case 429:
			return nil, &connectors.APIError{StatusCode: 429, Message: "Rate limit exceeded"}
		case 404:
			return nil, &connectors.APIError{StatusCode: 404, Message: "Model not found"}
		case 400:
			return nil, &connectors.APIError{StatusCode: 400, Message: "context_length_exceeded"}
		case 500:
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

	router := New(registry)

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
		openaiConnector.failureStatus = 429
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

}

func TestAPIKeyRestrictions(t *testing.T) {
	registry := &mockRegistry{
		connectors: map[string]connectors.Connector{
			"openai":    &mockConnector{name: "openai"},
			"anthropic": &mockConnector{name: "anthropic"},
			"groq":      &mockConnector{name: "groq"},
		},
	}

	router := New(registry)
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
