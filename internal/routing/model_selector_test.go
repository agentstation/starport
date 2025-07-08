package routing

import (
	"testing"

	"github.com/agentstation/starport/internal/connectors"
	"github.com/stretchr/testify/assert"
)

func TestModelCapabilities(t *testing.T) {
	caps := defaultModelCapabilities()
	
	// Test that we have capabilities for key models
	expectedModels := []string{
		"openai/gpt-4",
		"openai/gpt-3.5-turbo",
		"anthropic/claude-3-opus-20240229",
		"anthropic/claude-3-sonnet-20240229",
		"google-aistudio/gemini-1.5-pro",
		"groq/llama-3.1-8b-instant",
	}
	
	for _, model := range expectedModels {
		_, ok := caps[model]
		assert.True(t, ok, "Should have capabilities for %s", model)
	}
}

func TestIsModelAvailable(t *testing.T) {
	tests := []struct {
		name             string
		modelID          string
		requiredFeatures []string
		want             bool
	}{
		{
			name:             "model with vision supports vision",
			modelID:          "openai/gpt-4-vision-preview",
			requiredFeatures: []string{"vision"},
			want:             true,
		},
		{
			name:             "model without vision fails vision check",
			modelID:          "openai/gpt-3.5-turbo",
			requiredFeatures: []string{"vision"},
			want:             false,
		},
		{
			name:             "model with functions supports functions",
			modelID:          "openai/gpt-4",
			requiredFeatures: []string{"functions"},
			want:             true,
		},
		{
			name:             "anthropic models don't support functions",
			modelID:          "anthropic/claude-3-opus-20240229",
			requiredFeatures: []string{"functions"},
			want:             false,
		},
		{
			name:             "all models support streaming",
			modelID:          "groq/llama-3.1-8b-instant",
			requiredFeatures: []string{"streaming"},
			want:             true,
		},
		{
			name:             "unknown model is assumed available",
			modelID:          "unknown/model",
			requiredFeatures: []string{"vision", "functions"},
			want:             true,
		},
		{
			name:             "model with multiple features",
			modelID:          "openai/gpt-4-vision-preview",
			requiredFeatures: []string{"vision", "streaming"},
			want:             true,
		},
		{
			name:             "model fails if missing any required feature",
			modelID:          "anthropic/claude-3-opus-20240229",
			requiredFeatures: []string{"vision", "functions"},
			want:             false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsModelAvailable(tt.modelID, tt.requiredFeatures)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDefaultModelSelector_RequestAnalysis(t *testing.T) {
	selector := &defaultModelSelector{
		modelCapabilities: defaultModelCapabilities(),
	}
	
	t.Run("detect vision content", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{
						Role: "user",
						Content: []interface{}{
							map[string]interface{}{"type": "text", "text": "What's this?"},
							map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "http://example.com/image.jpg"}},
						},
					},
				},
			},
		}
		
		hasVision := selector.requestHasVision(req)
		assert.True(t, hasVision)
	})
	
	t.Run("detect function calling", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{Role: "user", Content: "Get weather"},
				},
				Tools: []connectors.Tool{
					{Type: "function", Function: connectors.Function{Name: "get_weather"}},
				},
			},
		}
		
		hasFunctions := selector.requestHasFunctions(req)
		assert.True(t, hasFunctions)
	})
	
	t.Run("estimate tokens", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{Role: "user", Content: "This is a test message with about 40 characters."},
					{Role: "assistant", Content: "This is another message with similar length here."},
				},
				MaxTokens: intPtr(100),
			},
		}
		
		tokens := selector.estimateTokens(req)
		// ~100 chars in messages / 4 + 100 max tokens * 4 chars / 4 = 25 + 100 = 125
		assert.Greater(t, tokens, 100)
		assert.Less(t, tokens, 200)
	})
}

func TestDefaultModelSelector_ModelSelection(t *testing.T) {
	selector := NewDefaultModelSelector()
	
	t.Run("simple request gets fast models first", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{Role: "user", Content: "What is 2+2?"},
				},
			},
		}
		
		models := selector.SelectModels(req)
		assert.NotEmpty(t, models)
		
		// First models should be fast/economical
		assert.Contains(t, models[0], "groq/llama-3.1-8b-instant")
		assert.Contains(t, models[1], "openai/gpt-3.5-turbo")
	})
	
	t.Run("vision request gets vision models", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{
						Role: "user",
						Content: []interface{}{
							map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "test.jpg"}},
						},
					},
				},
			},
		}
		
		models := selector.SelectModels(req)
		assert.NotEmpty(t, models)
		
		// Should get vision-capable models
		assert.Contains(t, models, "openai/gpt-4-vision-preview")
		assert.Contains(t, models, "anthropic/claude-3-sonnet-20240229")
		assert.Contains(t, models, "google-aistudio/gemini-1.5-pro")
	})
	
	t.Run("function request gets function models", func(t *testing.T) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{Role: "user", Content: "Call a function"},
				},
				Tools: []connectors.Tool{
					{Type: "function"},
				},
			},
		}
		
		models := selector.SelectModels(req)
		assert.NotEmpty(t, models)
		
		// Should include function-capable models
		hasOpenAI := false
		hasMistral := false
		for _, model := range models {
			if contains(model, "openai") {
				hasOpenAI = true
			}
			if contains(model, "mistral") {
				hasMistral = true
			}
		}
		assert.True(t, hasOpenAI, "Should include OpenAI models for functions")
		assert.True(t, hasMistral, "Should include Mistral models for functions")
	})
	
	t.Run("large context request", func(t *testing.T) {
		// Create a request with large estimated tokens
		largeContent := make([]byte, 50000) // ~12.5k tokens
		for i := range largeContent {
			largeContent[i] = 'a'
		}
		
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Messages: []connectors.Message{
					{Role: "user", Content: string(largeContent)},
				},
			},
		}
		
		models := selector.SelectModels(req)
		assert.NotEmpty(t, models)
		
		// Should not start with small context models
		assert.NotContains(t, models[0], "gpt-3.5")
	})
}

func TestDefaultModelSelector_Preferences(t *testing.T) {
	selector := NewDefaultModelSelector().(*defaultModelSelector)
	
	t.Run("quality preference", func(t *testing.T) {
		models := []string{
			"openai/gpt-3.5-turbo",
			"openai/gpt-4",
			"anthropic/claude-3-haiku-20240307",
			"anthropic/claude-3-opus-20240229",
		}
		
		metadata := &RequestMetadata{
			UserPreferences: map[string]interface{}{
				"quality": "premium",
			},
		}
		
		filtered := selector.filterByPreferences(models, metadata)
		
		// Should only include premium models
		for _, model := range filtered {
			cap, ok := selector.modelCapabilities[model]
			if ok {
				assert.Equal(t, "premium", cap.Quality, "Model %s should be premium", model)
			}
		}
	})
	
	t.Run("speed preference", func(t *testing.T) {
		models := []string{
			"openai/gpt-3.5-turbo",
			"openai/gpt-4",
			"groq/llama-3.1-8b-instant",
			"anthropic/claude-3-opus-20240229",
		}
		
		metadata := &RequestMetadata{
			UserPreferences: map[string]interface{}{
				"speed": "fast",
			},
		}
		
		filtered := selector.filterByPreferences(models, metadata)
		
		// Should only include fast models
		for _, model := range filtered {
			cap, ok := selector.modelCapabilities[model]
			if ok {
				assert.Equal(t, "fast", cap.LatencyClass, "Model %s should be fast", model)
			}
		}
	})
}

func TestDefaultModelSelector_Deduplication(t *testing.T) {
	selector := &defaultModelSelector{
		modelCapabilities: defaultModelCapabilities(),
	}
	
	models := []string{
		"openai/gpt-4",
		"anthropic/claude-3",
		"openai/gpt-4", // duplicate
		"groq/llama",
		"anthropic/claude-3", // duplicate
	}
	
	deduped := selector.deduplicateModels(models)
	
	assert.Len(t, deduped, 3)
	assert.Equal(t, []string{"openai/gpt-4", "anthropic/claude-3", "groq/llama"}, deduped)
}

// Helper function
func intPtr(i int) *int {
	return &i
}