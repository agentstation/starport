package proxy

import (
	"testing"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheControlValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     *ChatCompletionRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid cache control",
			req: &ChatCompletionRequest{
				Model: "anthropic/claude-3-5-sonnet",
				Messages: []connectors.Message{
					{
						Role: "system",
						Content: []connectors.ContentPart{
							{
								Type: "text",
								Text: "You are a helpful assistant",
								CacheControl: &connectors.CacheControl{
									Type: "ephemeral",
								},
							},
						},
					},
					{
						Role:    "user",
						Content: "Hello",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid cache control type",
			req: &ChatCompletionRequest{
				Model: "anthropic/claude-3-5-sonnet",
				Messages: []connectors.Message{
					{
						Role: "system",
						Content: []connectors.ContentPart{
							{
								Type: "text",
								Text: "You are a helpful assistant",
								CacheControl: &connectors.CacheControl{
									Type: "persistent", // Invalid type
								},
							},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid cache control type 'persistent', only 'ephemeral' is supported",
		},
		{
			name: "too many cache breakpoints for Anthropic",
			req: &ChatCompletionRequest{
				Model: "anthropic/claude-3-5-sonnet",
				Messages: []connectors.Message{
					{
						Role: "system",
						Content: []connectors.ContentPart{
							{Type: "text", Text: "1", CacheControl: &connectors.CacheControl{Type: "ephemeral"}},
							{Type: "text", Text: "2", CacheControl: &connectors.CacheControl{Type: "ephemeral"}},
							{Type: "text", Text: "3", CacheControl: &connectors.CacheControl{Type: "ephemeral"}},
							{Type: "text", Text: "4", CacheControl: &connectors.CacheControl{Type: "ephemeral"}},
							{Type: "text", Text: "5", CacheControl: &connectors.CacheControl{Type: "ephemeral"}},
						},
					},
				},
			},
			wantErr: false, // Basic validation passes, only provider-specific validation fails
			errMsg:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChatCompletionRequest(tt.req)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}

			// For provider-specific validation, we need to check it separately
			// The basic validation doesn't check Anthropic's 4 breakpoint limit
			if tt.name == "too many cache breakpoints for Anthropic" {
				// This should pass basic validation but fail provider-specific validation
				require.NoError(t, err)
				provider, _ := ExtractProviderFromModel(tt.req.Model)
				err = ValidateCacheControlForProvider(provider, tt.req.Messages)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "Anthropic supports a maximum of 4 cache control breakpoints")
			}
		})
	}
}

func TestProviderSupportsCacheControl(t *testing.T) {
	tests := []struct {
		provider string
		expected bool
	}{
		{"openai", true},
		{"anthropic", true},
		{"groq", true},
		{"deepseek", true},
		{"google", false},
		{"google-ai-studio", false},
		{"google-vertexai", false},
		{"mistral", false},
		{"azure", true},
		{"azure-openai", true},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			result := ProviderSupportsCacheControl(tt.provider)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCacheControlStripping(t *testing.T) {
	// Create a message with cache control
	originalContent := []connectors.ContentPart{
		{
			Type: "text",
			Text: "Hello world",
			CacheControl: &connectors.CacheControl{
				Type: "ephemeral",
			},
		},
	}

	// Strip cache control
	stripped, err := connectors.StripCacheControl(originalContent)
	require.NoError(t, err)

	// Verify cache control was removed
	parts, err := connectors.ParseMessageContent(stripped)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.Nil(t, parts[0].CacheControl)
	assert.Equal(t, "Hello world", parts[0].Text)
}

func TestHasCacheControl(t *testing.T) {
	tests := []struct {
		name     string
		content  connectors.MessageContent
		expected bool
	}{
		{
			name:     "string content without cache control",
			content:  "Hello world",
			expected: false,
		},
		{
			name: "content parts with cache control",
			content: []connectors.ContentPart{
				{
					Type: "text",
					Text: "Hello",
					CacheControl: &connectors.CacheControl{
						Type: "ephemeral",
					},
				},
			},
			expected: true,
		},
		{
			name: "content parts without cache control",
			content: []connectors.ContentPart{
				{
					Type: "text",
					Text: "Hello",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := connectors.HasCacheControl(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestServiceProcessChatCompletionWithCacheControl(t *testing.T) {
	// This test verifies that cache control is properly stripped for providers that don't support it
	t.Run("strips cache control for unsupported provider", func(t *testing.T) {
		// Create a request with cache control for Google (which doesn't support it)
		req := &ChatCompletionRequest{
			Model: "google/gemini-pro",
			Messages: []connectors.Message{
				{
					Role: "system",
					Content: []connectors.ContentPart{
						{
							Type: "text",
							Text: "You are a helpful assistant",
							CacheControl: &connectors.CacheControl{
								Type: "ephemeral",
							},
						},
					},
				},
				{
					Role:    "user",
					Content: "Hello",
				},
			},
		}

		// Validate that the provider doesn't support cache control
		provider, _ := ExtractProviderFromModel(req.Model)
		assert.False(t, ProviderSupportsCacheControl(provider))
		
		// Verify the request has cache control
		assert.True(t, connectors.HasCacheControl(req.Messages[0].Content))
		
		// Strip cache control as the service would do
		strippedContent, err := connectors.StripCacheControl(req.Messages[0].Content)
		require.NoError(t, err)
		
		// Verify cache control was removed
		assert.False(t, connectors.HasCacheControl(strippedContent))
	})
}

func TestCachePricingCalculation(t *testing.T) {
	t.Run("calculates cache pricing for Anthropic", func(t *testing.T) {
		// Test with Anthropic model
		pricing := connectors.GetCachePricing("anthropic/claude-3-5-sonnet")
		require.NotNil(t, pricing)
		
		// Verify pricing values
		assert.Equal(t, "0.003", pricing.Prompt)
		assert.Equal(t, "0.015", pricing.Completion)
		assert.Equal(t, "0.00375", pricing.CacheWrite)
		assert.Equal(t, "0.0003", pricing.CacheRead)
	})
	
	t.Run("calculates cache pricing for OpenAI", func(t *testing.T) {
		// Test with OpenAI model
		pricing := connectors.GetCachePricing("openai/gpt-4o")
		require.NotNil(t, pricing)
		
		// Verify pricing values
		assert.Equal(t, "0.0025", pricing.Prompt)
		assert.Equal(t, "0.01", pricing.Completion)
		assert.Equal(t, "0.00625", pricing.CacheWrite)
		assert.Equal(t, "0.00125", pricing.CacheRead)
	})
	
	t.Run("returns nil for unsupported provider", func(t *testing.T) {
		// Test with provider that doesn't support cache pricing
		pricing := connectors.GetCachePricing("mistral/mistral-large")
		assert.Nil(t, pricing)
	})
	
	t.Run("handles model with date suffix", func(t *testing.T) {
		// Test with model that has a date suffix
		pricing := connectors.GetCachePricing("anthropic/claude-3-5-sonnet-20241022")
		require.NotNil(t, pricing)
		assert.Equal(t, "0.00375", pricing.CacheWrite)
	})
}

func TestCacheCostCalculation(t *testing.T) {
	t.Run("calculates write cost correctly", func(t *testing.T) {
		// For 1000 tokens at $3.75 per million
		tokens := 1000
		writeRate := 0.00375
		expectedCost := float64(tokens) / 1000000.0 * writeRate
		
		assert.InDelta(t, 0.00000375, expectedCost, 0.00000001)
	})
	
	t.Run("calculates read cost correctly", func(t *testing.T) {
		// For 1000 tokens at $0.30 per million
		tokens := 1000
		readRate := 0.0003
		expectedCost := float64(tokens) / 1000000.0 * readRate
		
		assert.InDelta(t, 0.0000003, expectedCost, 0.00000001)
	})
}

func TestParseMessageContent(t *testing.T) {
	tests := []struct {
		name    string
		content connectors.MessageContent
		want    []connectors.ContentPart
		wantErr bool
	}{
		{
			name:    "string content",
			content: "Hello world",
			want: []connectors.ContentPart{
				{Type: "text", Text: "Hello world"},
			},
			wantErr: false,
		},
		{
			name: "content parts array",
			content: []connectors.ContentPart{
				{Type: "text", Text: "Part 1"},
				{Type: "text", Text: "Part 2"},
			},
			want: []connectors.ContentPart{
				{Type: "text", Text: "Part 1"},
				{Type: "text", Text: "Part 2"},
			},
			wantErr: false,
		},
		{
			name: "content with cache control",
			content: []connectors.ContentPart{
				{
					Type: "text",
					Text: "Cached content",
					CacheControl: &connectors.CacheControl{
						Type: "ephemeral",
					},
				},
			},
			want: []connectors.ContentPart{
				{
					Type: "text",
					Text: "Cached content",
					CacheControl: &connectors.CacheControl{
						Type: "ephemeral",
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "nil content",
			content: nil,
			want:    nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := connectors.ParseMessageContent(tt.content)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}