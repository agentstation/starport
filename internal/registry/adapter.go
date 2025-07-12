package registry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers/connectors"
)

// Adapter provides server-specific functionality on top of the Registry
type Adapter struct {
	registry *Registry
}

// NewAdapter creates a new registry adapter
func NewAdapter(registry *Registry) *Adapter {
	return &Adapter{
		registry: registry,
	}
}

// GetRegistry returns the underlying registry
func (a *Adapter) GetRegistry() *Registry {
	return a.registry
}

// InitializeFromConfig initializes connectors based on configuration
func (a *Adapter) InitializeFromConfig(ctx context.Context, cfg *config.Config) error {
	// Initialize OpenAI if configured
	if cfg.Providers.OpenAI.BaseURL != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.OpenAI, "OPENAI_API_KEY")
		connector, err := connectors.NewConnector("openai", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create OpenAI connector: %w", err)
		}
		if err := a.registry.Register("openai", connector); err != nil {
			return fmt.Errorf("failed to register OpenAI connector: %w", err)
		}
		log.Info().Msg("initialized OpenAI connector")
	}

	// Initialize Anthropic if configured
	if cfg.Providers.Anthropic.BaseURL != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.Anthropic, "ANTHROPIC_API_KEY")
		connector, err := connectors.NewConnector("anthropic", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Anthropic connector: %w", err)
		}
		if err := a.registry.Register("anthropic", connector); err != nil {
			return fmt.Errorf("failed to register Anthropic connector: %w", err)
		}
		log.Info().Msg("initialized Anthropic connector")
	}

	// Initialize Google AI Studio if configured
	if cfg.Providers.GoogleAIStudio.BaseURL != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.GoogleAIStudio, "GOOGLE_API_KEY")
		connector, err := connectors.NewConnector("google-aistudio", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Google AI Studio connector: %w", err)
		}
		if err := a.registry.Register("google-aistudio", connector); err != nil {
			return fmt.Errorf("failed to register Google AI Studio connector: %w", err)
		}
		log.Info().Msg("initialized Google AI Studio connector")
	} else if cfg.Providers.Gemini.BaseURL != "" {
		// Legacy support: Use deprecated Gemini config for Google AI Studio
		log.Warn().Msg("using deprecated Gemini config for Google AI Studio - please migrate to STARPORT_PROVIDERS_GOOGLE_AISTUDIO_*")
		providerCfg := convertToProviderConfig(cfg.Providers.Gemini, "GOOGLE_API_KEY")
		connector, err := connectors.NewConnector("google-aistudio", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Google AI Studio connector: %w", err)
		}
		if err := a.registry.Register("google-aistudio", connector); err != nil {
			return fmt.Errorf("failed to register Google AI Studio connector: %w", err)
		}
		log.Info().Msg("initialized Google AI Studio connector from deprecated Gemini config")
	}

	// Initialize Vertex AI if configured
	projectID := os.Getenv("STARPORT_PROVIDERS_GOOGLE_VERTEXAI_PROJECT_ID")
	if projectID == "" {
		projectID = os.Getenv("GCP_PROJECT_ID")
	}
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	
	if projectID != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.GoogleVertexAI, "GOOGLE_APPLICATION_CREDENTIALS")
		
		// Add Vertex AI specific configuration
		providerCfg.Extra = make(map[string]interface{})
		providerCfg.Extra["project_id"] = projectID
		
		// Check for location configuration
		location := os.Getenv("STARPORT_PROVIDERS_GOOGLE_VERTEXAI_LOCATION")
		if location != "" {
			providerCfg.Extra["location"] = location
		}
		
		// Check for fallback locations (comma-separated)
		fallbackLocations := os.Getenv("STARPORT_PROVIDERS_GOOGLE_VERTEXAI_FALLBACK_LOCATIONS")
		if fallbackLocations != "" {
			locations := strings.Split(fallbackLocations, ",")
			var fallbacks []interface{}
			for _, loc := range locations {
				fallbacks = append(fallbacks, strings.TrimSpace(loc))
			}
			providerCfg.Extra["fallback_locations"] = fallbacks
		}
		
		connector, err := connectors.NewConnector("google-vertexai", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Vertex AI connector: %w", err)
		}
		if err := a.registry.Register("google-vertexai", connector); err != nil {
			return fmt.Errorf("failed to register Vertex AI connector: %w", err)
		}
		logEntry := log.Info().Str("project_id", projectID)
		if loc, ok := providerCfg.Extra["location"].(string); ok {
			logEntry = logEntry.Str("location", loc)
		}
		logEntry.Msg("initialized Vertex AI connector")
	}

	// Note: Legacy Gemini config is mapped to Google AI Studio, not Vertex AI
	// Vertex AI requires additional configuration (project_id, location) that
	// the deprecated Gemini config doesn't provide

	// Initialize Groq if configured
	if cfg.Providers.Groq.BaseURL != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.Groq, "GROQ_API_KEY")
		connector, err := connectors.NewConnector("groq", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Groq connector: %w", err)
		}
		if err := a.registry.Register("groq", connector); err != nil {
			return fmt.Errorf("failed to register Groq connector: %w", err)
		}
		log.Info().Msg("initialized Groq connector")
	}

	// Initialize Mistral if configured
	if cfg.Providers.Mistral.BaseURL != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.Mistral, "MISTRAL_API_KEY")
		connector, err := connectors.NewConnector("mistral", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Mistral connector: %w", err)
		}
		if err := a.registry.Register("mistral", connector); err != nil {
			return fmt.Errorf("failed to register Mistral connector: %w", err)
		}
		log.Info().Msg("initialized Mistral connector")
	}

	// Initialize Azure OpenAI if configured
	if cfg.Providers.Azure.BaseURL != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.Azure, "AZURE_OPENAI_API_KEY")
		connector, err := connectors.NewConnector("azure", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Azure OpenAI connector: %w", err)
		}
		if err := a.registry.Register("azure-openai", connector); err != nil {
			return fmt.Errorf("failed to register Azure OpenAI connector: %w", err)
		}
		log.Info().Msg("initialized Azure OpenAI connector")
	}

	// If no providers are configured, use mock connector for development
	if len(a.registry.ListProviders()) == 0 {
		log.Warn().Msg("no providers configured, using mock connector")
		mockConfig := connectors.ProviderConfig{
			BaseURL: "http://mock",
			Timeout: 30 * time.Second,
		}
		mockConnector := connectors.NewMockConnector(mockConfig)
		if err := a.registry.Register("mock", mockConnector); err != nil {
			return fmt.Errorf("failed to register mock connector: %w", err)
		}
	}

	// Perform initial health checks
	healthResults := a.registry.HealthCheck(ctx)
	for provider, err := range healthResults {
		if err != nil {
			log.Warn().
				Err(err).
				Str("provider", provider).
				Msg("provider health check failed")
		} else {
			log.Info().
				Str("provider", provider).
				Msg("provider health check passed")
		}
	}

	return nil
}

// GetConnectorForModel returns the appropriate connector for a model ID
func (a *Adapter) GetConnectorForModel(modelID string) (connectors.Connector, string, error) {
	// Extract provider from model ID if present
	provider, model := extractProviderFromModel(modelID)

	if provider != "" {
		// Direct provider specified
		conn, err := a.registry.Get(provider)
		if err != nil {
			return nil, "", fmt.Errorf("provider not available: %s", provider)
		}
		return conn, model, nil
	}

	// No provider specified, need to determine from available providers
	// This would typically involve model routing logic
	// For now, return an error indicating model routing is needed
	return nil, "", fmt.Errorf("model routing required for model: %s", modelID)
}

// extractProviderFromModel extracts provider and model from a model ID
func extractProviderFromModel(modelID string) (provider, model string) {
	// Handle OpenRouter-style model IDs (provider/model)
	if idx := findProviderSeparator(modelID); idx != -1 {
		return modelID[:idx], modelID[idx+1:]
	}

	// No provider prefix
	return "", modelID
}

// findProviderSeparator finds the index of the provider separator
func findProviderSeparator(modelID string) int {
	// Look for the first "/" that separates provider from model
	for i, ch := range modelID {
		if ch == '/' {
			return i
		}
	}
	return -1
}

// getGoogleAPIKey returns the Google API key with correct precedence
// Priority order: STARPORT_PROVIDERS_GEMINI_API_KEY > GOOGLE_API_KEY > GEMINI_API_KEY
func getGoogleAPIKey() string {
	// First check the standard Starport environment variable
	if key := os.Getenv("STARPORT_PROVIDERS_GEMINI_API_KEY"); key != "" {
		return key
	}
	// Then check common alternative names
	if key := os.Getenv("GOOGLE_API_KEY"); key != "" {
		return key
	}
	return os.Getenv("GEMINI_API_KEY")
}

// convertToProviderConfig converts config.ProviderConfig to connectors.ProviderConfig
func convertToProviderConfig(cfg config.ProviderConfig, apiKeyEnv string) connectors.ProviderConfig {
	// Special handling for Google/Gemini API keys
	apiKey := ""
	if apiKeyEnv == "GOOGLE_API_KEY" {
		apiKey = getGoogleAPIKey()
	} else {
		apiKey = os.Getenv(apiKeyEnv)
	}

	return connectors.ProviderConfig{
		BaseURL:           cfg.BaseURL,
		Timeout:           cfg.Timeout,
		MaxConnections:    cfg.MaxConnections,
		MaxRetries:        cfg.MaxRetries,
		RetryDelay:        cfg.RetryDelay,
		BackoffMultiplier: cfg.BackoffMultiplier,
		APIKey:            apiKey,
	}
}
