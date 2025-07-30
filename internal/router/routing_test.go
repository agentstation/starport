package router

import (
	"context"
	"fmt"
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
			models:   []string{"openai/gpt-4", "anthropic/claude-3", "google-aistudio/gemini-pro"},
			prefs:    nil,
			expected: []string{"openai/gpt-4", "anthropic/claude-3", "google-aistudio/gemini-pro"},
		},
		{
			name:   "only filter",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-aistudio/gemini-pro"},
			prefs: &ProviderPreferences{
				Only: []string{"openai", "anthropic"},
			},
			expected: []string{"openai/gpt-4", "anthropic/claude-3"},
		},
		{
			name:   "ignore filter",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-aistudio/gemini-pro"},
			prefs: &ProviderPreferences{
				Ignore: []string{"google-aistudio"},
			},
			expected: []string{"openai/gpt-4", "anthropic/claude-3"},
		},
		{
			name:   "order without fallbacks",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-aistudio/gemini-pro", "groq/llama3-70b"},
			prefs: &ProviderPreferences{
				Order:          []string{"anthropic", "openai"},
				AllowFallbacks: false,
			},
			expected: []string{"anthropic/claude-3", "openai/gpt-4"},
		},
		{
			name:   "order with fallbacks",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-aistudio/gemini-pro", "groq/llama3-70b"},
			prefs: &ProviderPreferences{
				Order:          []string{"anthropic", "openai"},
				AllowFallbacks: true,
			},
			expected: []string{"anthropic/claude-3", "openai/gpt-4", "google-aistudio/gemini-pro", "groq/llama3-70b"},
		},
		{
			name:   "order with ignore",
			models: []string{"openai/gpt-4", "anthropic/claude-3", "google-aistudio/gemini-pro", "groq/llama3-70b"},
			prefs: &ProviderPreferences{
				Order:          []string{"google", "anthropic", "openai"},
				Ignore:         []string{"google-aistudio"},
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

func TestProviderHealthTracking(t *testing.T) {
	registry := &mockRegistry{
		connectors: make(map[string]connectors.Connector),
	}
	router := New(registry).(*modelRouter)

	// Test initial state - provider should be healthy
	assert.True(t, router.isProviderHealthy("openai"))

	// Record failures
	router.recordProviderFailure("openai", fmt.Errorf("connection error"))
	assert.True(t, router.isProviderHealthy("openai")) // Still healthy after 1 failure

	router.recordProviderFailure("openai", fmt.Errorf("connection error"))
	assert.True(t, router.isProviderHealthy("openai")) // Still healthy after 2 failures

	router.recordProviderFailure("openai", fmt.Errorf("connection error"))
	assert.False(t, router.isProviderHealthy("openai")) // Circuit open after 3 failures

	// Record success - should reset
	router.recordProviderSuccess("openai")
	assert.True(t, router.isProviderHealthy("openai"))
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

func TestLatencyBasedSelection(t *testing.T) {
	tracker := NewLatencyTracker(0.2, 1)
	selector := NewLatencyBasedSelector(tracker, 2.0)

	// No latency data - should return first provider
	providers := []string{"openai", "anthropic", "google"}
	selected := selector.SelectProvider(providers)
	assert.Equal(t, "openai", selected)

	// Add latency data
	tracker.RecordLatency("openai", 200*time.Millisecond)
	tracker.RecordLatency("anthropic", 100*time.Millisecond)
	tracker.RecordLatency("google", 300*time.Millisecond)

	// Should select provider with lowest latency
	selected = selector.SelectProvider(providers)
	assert.Equal(t, "anthropic", selected)

	// Test filtering by latency threshold
	filtered := selector.FilterByLatency(providers)
	// All should be within 2x of minimum (100ms), so two should be included (anthropic=100ms, openai=200ms)
	// google=300ms is > 2 * 100ms so it should be filtered out
	assert.Len(t, filtered, 2)
	assert.Contains(t, filtered, "anthropic")
	assert.Contains(t, filtered, "openai")
	assert.NotContains(t, filtered, "google")

	// Add a very slow provider
	tracker.RecordLatency("slow", 500*time.Millisecond)
	providers = []string{"openai", "anthropic", "google", "slow"}
	filtered = selector.FilterByLatency(providers)
	// "slow" and "google" should be filtered out (both > 2 * 100ms)
	assert.Len(t, filtered, 2)
	assert.NotContains(t, filtered, "slow")
	assert.NotContains(t, filtered, "google")
}

func TestCostCalculation(t *testing.T) {
	calculator := NewCostCalculator()

	// Test known model
	pricing, exists := calculator.GetModelCost("openai/gpt-4")
	assert.True(t, exists)
	assert.Greater(t, pricing.PromptCostPer1M, 0.0)
	assert.Greater(t, pricing.CompletionCostPer1M, 0.0)

	// Test cost estimation
	cost := calculator.EstimateCost("openai/gpt-3.5-turbo", 1000, 500)
	// 1000 prompt tokens at $0.5/1M + 500 completion tokens at $1.5/1M
	expectedCost := (1000.0/1_000_000)*0.5 + (500.0/1_000_000)*1.5
	assert.InDelta(t, expectedCost, cost, 0.0001)

	// Test cost comparison
	ratio := calculator.CompareModelCosts("openai/gpt-4", "openai/gpt-3.5-turbo", 1000, 500)
	assert.Greater(t, ratio, 1.0) // GPT-4 should be more expensive

	// Test unknown model
	_, exists = calculator.GetModelCost("unknown/model")
	assert.False(t, exists)
}

func TestCostOptimizedSelection(t *testing.T) {
	calculator := NewCostCalculator()
	tracker := NewLatencyTracker(0.2, 1)
	selector := NewCostOptimizedSelector(calculator, tracker, 2.0, 2.0)

	// Add some latency data
	tracker.RecordLatency("openai", 100*time.Millisecond)
	tracker.RecordLatency("groq", 50*time.Millisecond)
	tracker.RecordLatency("anthropic", 150*time.Millisecond)

	models := []string{
		"openai/gpt-4",                     // Expensive, medium latency
		"openai/gpt-3.5-turbo",             // Cheap, medium latency
		"groq/llama3-70b-8192",             // Very cheap, low latency
		"anthropic/claude-3-opus-20240229", // Very expensive, high latency
	}

	// Should prefer groq/llama3 due to low cost and low latency
	selected := selector.SelectModel(models, 1000, 500)
	assert.Equal(t, "groq/llama3-70b-8192", selected)
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

func TestEnhancedAPIKeyRestrictions(t *testing.T) {
	registry := &mockRegistry{
		connectors: make(map[string]connectors.Connector),
	}
	router := New(registry).(*modelRouter)

	models := []string{"openai/gpt-4", "anthropic/claude-3", "google-aistudio/gemini-pro"}
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
	assert.NotContains(t, filtered, "google-aistudio/gemini-pro") // Not allowed
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
		name:        "openai",
		shouldFail:  true,
		failureType: FallbackRateLimit,
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
	assert.Contains(t, resp.Metadata.ModelsAttempted[0].Error, "Rate limit exceeded")
	assert.Equal(t, "success", resp.Metadata.ModelsAttempted[1].Status)
}
