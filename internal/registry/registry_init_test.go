package registry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestNewRegistry_NoConfig(t *testing.T) {
	// Test creating registry with nil config
	reg, err := New(nil)
	require.NoError(t, err)
	require.NotNil(t, reg)

	// Should have mock provider
	providers := reg.ListProviders()
	assert.Contains(t, providers, "mock")
}

func TestNewRegistry_EmptyProviders(t *testing.T) {
	// Test creating registry with empty providers config
	cfg := &Config{
		Providers: &config.ProvidersConfig{},
	}
	
	reg, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, reg)

	// Should have mock provider
	providers := reg.ListProviders()
	assert.Contains(t, providers, "mock")
}

func TestNewRegistry_WithProviders(t *testing.T) {
	// Set test API keys
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	cfg := &Config{
		Providers: &config.ProvidersConfig{
			OpenAI: config.ProviderConfig{
				BaseURL: "https://api.openai.com/v1",
				Timeout: 30 * time.Second,
			},
		},
		HealthCheckOnInit: false, // Skip health check in test
	}
	
	reg, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, reg)

	// Should have OpenAI provider
	providers := reg.ListProviders()
	assert.Contains(t, providers, "openai")
}

func TestNewRegistry_GoogleProviders(t *testing.T) {
	tests := []struct {
		name      string
		setup     func()
		cleanup   func()
		providers *config.ProvidersConfig
		expected  []string
	}{
		{
			name: "Google AI Studio via GoogleAIStudio config",
			setup: func() {
				os.Setenv("GOOGLE_API_KEY", "test-key")
			},
			cleanup: func() {
				os.Unsetenv("GOOGLE_API_KEY")
			},
			providers: &config.ProvidersConfig{
				GoogleAIStudio: config.ProviderConfig{
					BaseURL: "https://generativelanguage.googleapis.com/v1beta",
				},
			},
			expected: []string{"google-ai-studio"},
		},
		{
			name: "Google AI Studio via legacy Gemini config",
			setup: func() {
				os.Setenv("STARPORT_PROVIDERS_GEMINI_API_KEY", "test-key")
			},
			cleanup: func() {
				os.Unsetenv("STARPORT_PROVIDERS_GEMINI_API_KEY")
			},
			providers: &config.ProvidersConfig{
				Gemini: config.ProviderConfig{
					BaseURL: "https://generativelanguage.googleapis.com/v1beta",
				},
			},
			expected: []string{"google-ai-studio"},
		},
		{
			name: "Vertex AI",
			setup: func() {
				os.Setenv("STARPORT_PROVIDERS_GOOGLE_VERTEXAI_PROJECT_ID", "test-project")
				os.Setenv("STARPORT_PROVIDERS_GOOGLE_VERTEXAI_LOCATION", "us-central1")
			},
			cleanup: func() {
				os.Unsetenv("STARPORT_PROVIDERS_GOOGLE_VERTEXAI_PROJECT_ID")
				os.Unsetenv("STARPORT_PROVIDERS_GOOGLE_VERTEXAI_LOCATION")
			},
			providers: &config.ProvidersConfig{
				GoogleVertexAI: config.ProviderConfig{
					BaseURL: "https://us-central1-aiplatform.googleapis.com",
				},
			},
			expected: []string{"google-vertex"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			defer tt.cleanup()

			cfg := &Config{
				Providers:         tt.providers,
				HealthCheckOnInit: false,
			}
			
			reg, err := New(cfg)
			require.NoError(t, err)
			require.NotNil(t, reg)

			providers := reg.ListProviders()
			for _, expected := range tt.expected {
				assert.Contains(t, providers, expected)
			}
		})
	}
}

func TestNewRegistry_MultipleProviders(t *testing.T) {
	// Set test API keys
	os.Setenv("OPENAI_API_KEY", "test-key1")
	os.Setenv("ANTHROPIC_API_KEY", "test-key2")
	os.Setenv("GROQ_API_KEY", "test-key3")
	defer func() {
		os.Unsetenv("OPENAI_API_KEY")
		os.Unsetenv("ANTHROPIC_API_KEY")
		os.Unsetenv("GROQ_API_KEY")
	}()

	cfg := &Config{
		Providers: &config.ProvidersConfig{
			OpenAI: config.ProviderConfig{
				BaseURL: "https://api.openai.com/v1",
			},
			Anthropic: config.ProviderConfig{
				BaseURL: "https://api.anthropic.com",
			},
			Groq: config.ProviderConfig{
				BaseURL: "https://api.groq.com/openai/v1",
			},
		},
		HealthCheckOnInit: false,
	}
	
	reg, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, reg)

	// Should have all providers
	providers := reg.ListProviders()
	assert.Contains(t, providers, "openai")
	assert.Contains(t, providers, "anthropic")
	assert.Contains(t, providers, "groq")
}

func TestRegistry_GetConnectorForModel(t *testing.T) {
	reg := NewEmpty()
	
	// Add mock connector
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	err := reg.Register("mock", mockConnector)
	require.NoError(t, err)

	// Test getting connector for model
	conn, modelID, err := reg.GetConnectorForModel("mock/test-model")
	require.NoError(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, "mock/test-model", modelID)
}

func TestRegistry_HealthCheck(t *testing.T) {
	reg := NewEmpty()
	
	// Add mock connector
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	err := reg.Register("mock", mockConnector)
	require.NoError(t, err)

	// Perform health check
	ctx := context.Background()
	results := reg.HealthCheck(ctx)
	
	// Mock connector should pass health check
	assert.Contains(t, results, "mock")
	assert.NoError(t, results["mock"])
}

func TestNewEmpty(t *testing.T) {
	reg := NewEmpty()
	require.NotNil(t, reg)
	
	// Should have no providers
	providers := reg.ListProviders()
	assert.Empty(t, providers)
}

