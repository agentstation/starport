package connectors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetProviderMetadata(t *testing.T) {
	providers := GetProviderMetadata()

	// Should return all 7 providers (including google-aistudio and google-vertexai)
	assert.Equal(t, 7, len(providers))

	// Check each provider has required fields (OpenRouter format)
	for _, provider := range providers {
		assert.NotEmpty(t, provider.Name)
		assert.NotEmpty(t, provider.Slug)
		assert.NotEmpty(t, provider.PrivacyPolicyURL)
		// Boolean fields exist by default
		// MayLogPrompts, MayTrainOnData, ModeratedByOpenRouter can be false
	}

	// Check specific providers
	providerMap := make(map[string]ProviderMetadata)
	for _, p := range providers {
		providerMap[p.Slug] = p
	}

	// OpenAI
	openai, ok := providerMap["openai"]
	require.True(t, ok)
	assert.Equal(t, "OpenAI", openai.Name)
	assert.False(t, openai.MayLogPrompts)
	assert.False(t, openai.MayTrainOnData)
	assert.False(t, openai.ModeratedByOpenRouter)
	assert.Equal(t, "https://openai.com/privacy", openai.PrivacyPolicyURL)
	assert.Equal(t, "https://openai.com/terms", openai.TermsOfServiceURL)
	assert.Equal(t, "https://status.openai.com", openai.StatusPageURL)

	// Anthropic
	anthropic, ok := providerMap["anthropic"]
	require.True(t, ok)
	assert.Equal(t, "Anthropic", anthropic.Name)
	assert.False(t, anthropic.MayLogPrompts)
	assert.False(t, anthropic.MayTrainOnData)
	assert.Equal(t, "https://www.anthropic.com/privacy", anthropic.PrivacyPolicyURL)

	// Azure
	azure, ok := providerMap["azure"]
	require.True(t, ok)
	assert.Equal(t, "Azure OpenAI", azure.Name)
	assert.Equal(t, "https://azure.microsoft.com/status", azure.StatusPageURL)
}

func TestGetModelMetadata(t *testing.T) {
	tests := []struct {
		name     string
		modelID  string
		expected struct {
			hasMetadata bool
			name        string
			hasContext  bool
			context     int
			hasPricing  bool
		}
	}{
		{
			name:    "known model - openai/gpt-4",
			modelID: "openai/gpt-4",
			expected: struct {
				hasMetadata bool
				name        string
				hasContext  bool
				context     int
				hasPricing  bool
			}{
				hasMetadata: true,
				name:        "OpenAI: GPT-4",
				hasContext:  true,
				context:     8191,
				hasPricing:  true,
			},
		},
		{
			name:    "known model - anthropic/claude-3-opus",
			modelID: "anthropic/claude-3-opus",
			expected: struct {
				hasMetadata bool
				name        string
				hasContext  bool
				context     int
				hasPricing  bool
			}{
				hasMetadata: true,
				name:        "Anthropic: Claude 3 Opus",
				hasContext:  true,
				context:     200000,
				hasPricing:  true,
			},
		},
		{
			name:    "unknown model",
			modelID: "unknown/model",
			expected: struct {
				hasMetadata bool
				name        string
				hasContext  bool
				context     int
				hasPricing  bool
			}{
				hasMetadata: false,
				name:        "unknown/model",
				hasContext:  false,
				hasPricing:  false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := GetModelMetadata(tt.modelID)
			require.NotNil(t, metadata)

			assert.Equal(t, tt.modelID, metadata.ID)

			if tt.expected.hasMetadata {
				assert.Equal(t, tt.expected.name, metadata.Name)
				assert.NotEmpty(t, metadata.Description)

				if tt.expected.hasContext {
					require.NotNil(t, metadata.Context)
					assert.Equal(t, ModelContext(tt.expected.context), *metadata.Context)
				}

				if tt.expected.hasPricing {
					require.NotNil(t, metadata.Pricing)
					assert.NotEmpty(t, metadata.Pricing.Prompt)
					assert.NotEmpty(t, metadata.Pricing.Completion)
				}

				// Check architecture
				require.NotNil(t, metadata.Architecture)
				assert.NotEmpty(t, metadata.Architecture.InputModalities)
				assert.NotEmpty(t, metadata.Architecture.OutputModalities)
				assert.NotEmpty(t, metadata.Architecture.Tokenizer)

				// Check supported parameters
				assert.NotEmpty(t, metadata.SupportedParameters)
			} else {
				// Unknown model should have basic info
				assert.Equal(t, tt.expected.name, metadata.Name)
				assert.Equal(t, tt.modelID, metadata.ID)
			}
		})
	}
}

func TestGetModelsByProvider(t *testing.T) {
	tests := []struct {
		provider     string
		expectModels []string
		minCount     int
	}{
		{
			provider: "openai",
			expectModels: []string{
				"openai/gpt-4",
				"openai/gpt-4-turbo",
				"openai/gpt-3.5-turbo",
			},
			minCount: 3,
		},
		{
			provider: "anthropic",
			expectModels: []string{
				"anthropic/claude-3-opus",
				"anthropic/claude-3-sonnet",
				"anthropic/claude-3-haiku",
			},
			minCount: 3,
		},
		{
			provider: "google",
			expectModels: []string{
				"google/gemini-pro-1.5",
				"google/gemini-flash-1.5",
			},
			minCount: 10,
		},
		{
			provider:     "unknown",
			expectModels: []string{},
			minCount:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			models := GetModelsByProvider(tt.provider)
			assert.GreaterOrEqual(t, len(models), tt.minCount)

			for _, expectedModel := range tt.expectModels {
				assert.Contains(t, models, expectedModel)
			}
		})
	}
}

func TestGetProvidersForModel(t *testing.T) {
	tests := []struct {
		modelName      string
		expectCount    int
		expectProvider string
	}{
		{
			modelName:      "gpt-4",
			expectCount:    1, // Only OpenAI in catalog
			expectProvider: "openai",
		},
		{
			modelName:      "claude-3-opus",
			expectCount:    1, // Only Anthropic
			expectProvider: "anthropic",
		},
		{
			modelName:      "nonexistent-model",
			expectCount:    0,
			expectProvider: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.modelName, func(t *testing.T) {
			providers := GetProvidersForModel(tt.modelName)
			assert.Equal(t, tt.expectCount, len(providers))

			if tt.expectCount > 0 {
				// Check that expected provider is in the list
				found := false
				for _, p := range providers {
					if p.Provider == tt.expectProvider {
						found = true
						assert.NotEmpty(t, p.Name)
						// Model like gpt-4 should have pricing
						if p.Pricing != nil {
							assert.NotEmpty(t, p.Pricing.Prompt)
							assert.NotEmpty(t, p.Pricing.Completion)
						}
						break
					}
				}
				assert.True(t, found, "Expected provider %s not found", tt.expectProvider)
			}
		})
	}
}

func TestModelContextPtr(t *testing.T) {
	// Test the helper function
	contextValue := 8192
	ptr := modelContextPtr(contextValue)

	require.NotNil(t, ptr)
	assert.Equal(t, ModelContext(contextValue), *ptr)
}
