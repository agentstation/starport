package router

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderPreferencesFiltering(t *testing.T) {
	tests := []struct {
		name     string
		models   []string
		prefs    *ProviderPreferences
		expected []string
	}{
		{
			name:     "no preferences",
			models:   []string{"openai/gpt-4", "anthropic/claude-3", "google-ai-studio/gemini-pro"},
			prefs:    nil,
			expected: []string{"openai/gpt-4", "anthropic/claude-3", "google-ai-studio/gemini-pro"},
		},
		{
			name:   "only filter",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-ai-studio/gemini-pro"},
			prefs: &ProviderPreferences{
				Only: []string{"openai", "anthropic"},
			},
			expected: []string{"openai/gpt-4", "anthropic/claude-3"},
		},
		{
			name:   "ignore filter",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-ai-studio/gemini-pro"},
			prefs: &ProviderPreferences{
				Ignore: []string{"google-ai-studio"},
			},
			expected: []string{"openai/gpt-4", "anthropic/claude-3"},
		},
		{
			name:   "order without fallbacks",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-ai-studio/gemini-pro", "groq/llama3-70b"},
			prefs: &ProviderPreferences{
				Order:          []string{"anthropic", "openai"},
				AllowFallbacks: false,
			},
			expected: []string{"anthropic/claude-3", "openai/gpt-4"},
		},
		{
			name:   "order with fallbacks",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-ai-studio/gemini-pro", "groq/llama3-70b"},
			prefs: &ProviderPreferences{
				Order:          []string{"anthropic", "openai"},
				AllowFallbacks: true,
			},
			expected: []string{"anthropic/claude-3", "openai/gpt-4", "google-ai-studio/gemini-pro", "groq/llama3-70b"},
		},
		{
			name:   "order with ignore",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-ai-studio/gemini-pro", "groq/llama3-70b"},
			prefs: &ProviderPreferences{
				Order:          []string{"google-ai-studio", "anthropic", "openai"},
				Ignore:         []string{"google-ai-studio"},
				AllowFallbacks: true,
			},
			expected: []string{"anthropic/claude-3", "openai/gpt-4", "groq/llama3-70b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &mockRegistry{
				connectors: make(map[string]connectors.Connector),
			}
			router := New(registry).(*modelRouter)

			filtered := router.filterByProviderPreferences(tt.models, tt.prefs)
			assert.Equal(t, tt.expected, filtered)
		})
	}
}

func TestLatencyTracking(t *testing.T) {
	tracker := NewLatencyTracker(0.2, 3)

	// No data initially
	assert.Equal(t, time.Duration(0), tracker.GetLatency("openai"))

	// Record some latencies
	tracker.RecordLatency("openai", 100*time.Millisecond)
	tracker.RecordLatency("openai", 200*time.Millisecond)
	tracker.RecordLatency("openai", 150*time.Millisecond)

	// Check average during warmup (should be (100+200+150)/3 = 150ms)
	latency := tracker.GetLatency("openai")
	assert.InDelta(t, 150, latency.Milliseconds(), 1)

	// Record more to trigger EMA
	tracker.RecordLatency("openai", 300*time.Millisecond)
	latency = tracker.GetLatency("openai")
	// EMA should be: 0.2*300 + 0.8*150 = 60 + 120 = 180ms
	assert.InDelta(t, 180, latency.Milliseconds(), 1)

	// Test multiple providers
	tracker.RecordLatency("anthropic", 50*time.Millisecond)
	tracker.RecordLatency("google", 250*time.Millisecond)

	allLatencies := tracker.GetAllLatencies()
	assert.Len(t, allLatencies, 3)
	assert.Contains(t, allLatencies, "openai")
	assert.Contains(t, allLatencies, "anthropic")
	assert.Contains(t, allLatencies, "google")
}

func TestStickySession(t *testing.T) {
	manager := NewStickyProviderSessionManager(1 * time.Second)

	// No session initially
	provider, exists := manager.GetProvider("conv-123")
	assert.False(t, exists)
	assert.Empty(t, provider)

	// Set session
	manager.SetProvider("conv-123", "openai")
	provider, exists = manager.GetProvider("conv-123")
	assert.True(t, exists)
	assert.Equal(t, "openai", provider)

	// Different conversation
	provider, exists = manager.GetProvider("conv-456")
	assert.False(t, exists)

	// Remove session
	manager.RemoveSession("conv-123")
	provider, exists = manager.GetProvider("conv-123")
	assert.False(t, exists)

	// Test expiration
	manager.SetProvider("conv-789", "anthropic")
	time.Sleep(1100 * time.Millisecond)
	provider, exists = manager.GetProvider("conv-789")
	assert.False(t, exists) // Should be expired
}

func TestStickySessionStorageIsBounded(t *testing.T) {
	manager := NewStickyProviderSessionManager(time.Hour).(*memoryStickyProviderSessionManager)
	manager.maximum = 2
	manager.SetProvider("conv-1", "openai")
	manager.SetProvider("conv-2", "anthropic")
	manager.SetProvider("conv-3", "openai")

	manager.mu.RLock()
	require.Len(t, manager.sessions, 2)
	manager.mu.RUnlock()
	provider, exists := manager.GetProvider("conv-3")
	require.True(t, exists)
	require.Equal(t, "openai", provider)
}

func TestEnhancedAPIKeyRestrictions(t *testing.T) {
	registry := &mockRegistry{
		connectors: make(map[string]connectors.Connector),
	}
	router := New(registry).(*modelRouter)

	models := []string{"openai/gpt-4", "anthropic/claude-3", "google-ai-studio/gemini-pro"}
	config := &APIKeyConfig{
		AllowedProviders: []string{"openai", "anthropic"},
		ModelOverrides: map[string]string{
			"openai/gpt-4": "openai/gpt-3.5-turbo", // Downgrade GPT-4 to 3.5
		},
	}

	filtered := router.filterByAPIKeyRestrictions(models, config)
	assert.Len(t, filtered, 2)
	assert.Contains(t, filtered, "openai/gpt-3.5-turbo") // Override applied
	assert.Contains(t, filtered, "anthropic/claude-3")
	assert.NotContains(t, filtered, "google-ai-studio/gemini-pro") // Not allowed

	modelConfig := &APIKeyConfig{
		AllowedModels: []string{"openai/gpt-4", "claude-3"},
	}

	filtered = router.filterByAPIKeyRestrictions(models, modelConfig)
	assert.ElementsMatch(t, []string{"openai/gpt-4", "anthropic/claude-3"}, filtered)

	combinedConfig := &APIKeyConfig{
		AllowedProviders: []string{"openai", "anthropic"},
		AllowedModels:    []string{"openai/gpt-4", "google-ai-studio/gemini-pro"},
	}

	filtered = router.filterByAPIKeyRestrictions(models, combinedConfig)
	assert.Equal(t, []string{"openai/gpt-4"}, filtered)
}

func TestRouterWithStickySessions(t *testing.T) {
	ctx := context.Background()
	registry := &mockRegistry{
		connectors: make(map[string]connectors.Connector),
	}

	// Register mock connectors
	openaiConnector := &mockConnector{
		name: "openai",
		chatFunc: func(_ context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			return &connectors.ChatResponse{
				ID:    "openai-response",
				Model: req.Model,
			}, nil
		},
	}

	anthropicConnector := &mockConnector{
		name: "anthropic",
		chatFunc: func(_ context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			return &connectors.ChatResponse{
				ID:    "anthropic-response",
				Model: req.Model,
			}, nil
		},
	}

	registry.connectors["openai"] = openaiConnector
	registry.connectors["anthropic"] = anthropicConnector

	// Router now includes all features by default
	router := New(registry)

	// First request - should use first available
	req := &Request{
		ChatRequest: &connectors.ChatRequest{
			Model: "openai/gpt-4",
		},
		Models: []string{"openai/gpt-4", "anthropic/claude-3"},
		Metadata: &RequestMetadata{
			ConversationID: "conv-123",
		},
	}

	resp, err := router.RouteWithFallback(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "openai/gpt-4", resp.ModelUsed)

	// Second request with same conversation - should stick to openai
	req.Models = []string{"anthropic/claude-3", "openai/gpt-4"} // Different order
	modelID, connector, err := router.SelectModel(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "openai/gpt-4", modelID)
	assert.NotNil(t, connector)
}

func TestRouterCompleteFlow(t *testing.T) {
	ctx := context.Background()
	registry := &mockRegistry{
		connectors: make(map[string]connectors.Connector),
	}

	// Set up various connectors
	rateLimitedConnector := &mockConnector{
		name:          "openai",
		shouldFail:    true,
		failureStatus: 429,
	}

	expensiveConnector := &mockConnector{
		name: "anthropic",
		chatFunc: func(_ context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			time.Sleep(100 * time.Millisecond)
			return &connectors.ChatResponse{
				ID:    "expensive-response",
				Model: req.Model,
			}, nil
		},
	}

	cheapConnector := &mockConnector{
		name: "groq",
		chatFunc: func(_ context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			time.Sleep(50 * time.Millisecond)
			return &connectors.ChatResponse{
				ID:    "cheap-response",
				Model: req.Model,
			}, nil
		},
	}

	registry.connectors["openai"] = rateLimitedConnector
	registry.connectors["anthropic"] = expensiveConnector
	registry.connectors["groq"] = cheapConnector

	// Router now includes all features by default
	router := New(registry)

	// First request
	req := &Request{
		ChatRequest: &connectors.ChatRequest{
			Messages: []connectors.Message{{Role: "user", Content: "Hello"}},
		},
		Models: []string{
			"openai/gpt-4",
			"anthropic/claude-3-sonnet-20240229",
			"groq/llama3-70b-8192",
		},
		ProviderPreferences: &ProviderPreferences{
			Order:          []string{"openai", "anthropic", "groq"},
			AllowFallbacks: true,
		},
		Metadata: &RequestMetadata{
			ConversationID:  "test-conv",
			EstimatedTokens: 500,
		},
	}

	resp, err := router.RouteWithFallback(ctx, req)
	require.NoError(t, err)

	// Should fallback from rate-limited openai to anthropic
	assert.Equal(t, "anthropic/claude-3-sonnet-20240229", resp.ModelUsed)
	assert.Equal(t, "anthropic", resp.ProviderUsed)
	assert.Equal(t, 2, resp.Attempts)

	// Check metadata
	assert.Len(t, resp.Metadata.ModelsAttempted, 2)
	assert.Equal(t, "failed", resp.Metadata.ModelsAttempted[0].Status)
	assert.Equal(t, "The provider rate limit was reached.", resp.Metadata.ModelsAttempted[0].Error)
	assert.Equal(t, "success", resp.Metadata.ModelsAttempted[1].Status)
}
