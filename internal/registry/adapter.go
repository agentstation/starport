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
	"github.com/agentstation/starport/pkg/catalog"
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

// providerInit holds initialization data for a provider
type providerInit struct {
	name      string
	baseURL   string
	apiKeyEnv string
}

// InitializeFromConfig initializes connectors based on configuration
func (a *Adapter) InitializeFromConfig(ctx context.Context, cfg *config.Config) error {
	// Define provider configurations
	providers := []providerInit{
		{"openai", cfg.Providers.OpenAI.BaseURL, "OPENAI_API_KEY"},
		{"anthropic", cfg.Providers.Anthropic.BaseURL, "ANTHROPIC_API_KEY"},
		{"groq", cfg.Providers.Groq.BaseURL, "GROQ_API_KEY"},
		{"mistral", cfg.Providers.Mistral.BaseURL, "MISTRAL_API_KEY"},
	}

	// Initialize standard providers
	for _, p := range providers {
		if err := a.initializeProvider(p, cfg); err != nil {
			return err
		}
	}

	// Handle Google providers separately due to special logic
	if err := a.initializeGoogleProviders(cfg); err != nil {
		return err
	}

	// Initialize Azure OpenAI if configured
	if cfg.Providers.Azure.BaseURL != "" {
		if err := a.initializeAzureProvider(cfg); err != nil {
			return err
		}
	}

	// If no providers are configured, use mock connector for development
	if len(a.registry.ListProviders()) == 0 {
		if err := a.initializeMockProvider(); err != nil {
			return err
		}
	}

	// Perform initial health checks
	a.performHealthChecks(ctx)

	return nil
}

// initializeProvider initializes a standard provider
func (a *Adapter) initializeProvider(p providerInit, cfg *config.Config) error {
	if p.baseURL == "" {
		return nil
	}

	var providerCfg connectors.ProviderConfig
	switch p.name {
	case "openai":
		providerCfg = convertToProviderConfig(cfg.Providers.OpenAI, p.apiKeyEnv)
	case "anthropic":
		providerCfg = convertToProviderConfig(cfg.Providers.Anthropic, p.apiKeyEnv)
	case "groq":
		providerCfg = convertToProviderConfig(cfg.Providers.Groq, p.apiKeyEnv)
	case "mistral":
		providerCfg = convertToProviderConfig(cfg.Providers.Mistral, p.apiKeyEnv)
	}

	connector, err := connectors.NewConnector(p.name, providerCfg)
	if err != nil {
		return fmt.Errorf("failed to create %s connector: %w", p.name, err)
	}
	if err := a.registry.Register(p.name, connector); err != nil {
		return fmt.Errorf("failed to register %s connector: %w", p.name, err)
	}
	log.Info().Msgf("initialized %s connector", p.name)
	return nil
}

// initializeGoogleProviders handles Google AI Studio and Vertex AI initialization
func (a *Adapter) initializeGoogleProviders(cfg *config.Config) error {
	// Initialize Google AI Studio if configured
	if cfg.Providers.GoogleAIStudio.BaseURL != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.GoogleAIStudio, "GOOGLE_API_KEY")
		connector, err := connectors.NewConnector("google-ai-studio", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Google AI Studio connector: %w", err)
		}
		if err := a.registry.Register("google-ai-studio", connector); err != nil {
			return fmt.Errorf("failed to register Google AI Studio connector: %w", err)
		}
		log.Info().Msg("initialized Google AI Studio connector")
	} else if cfg.Providers.Gemini.BaseURL != "" {
		// Legacy support: Use deprecated Gemini config for Google AI Studio
		log.Warn().Msg("using deprecated Gemini config for Google AI Studio - please migrate to STARPORT_PROVIDERS_GOOGLE_AISTUDIO_*")
		providerCfg := convertToProviderConfig(cfg.Providers.Gemini, "GOOGLE_API_KEY")
		connector, err := connectors.NewConnector("google-ai-studio", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Google AI Studio connector: %w", err)
		}
		if err := a.registry.Register("google-ai-studio", connector); err != nil {
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
		
		connector, err := connectors.NewConnector("google-vertex", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Vertex AI connector: %w", err)
		}
		if err := a.registry.Register("google-vertex", connector); err != nil {
			return fmt.Errorf("failed to register Vertex AI connector: %w", err)
		}
		logEntry := log.Info().Str("project_id", projectID)
		if loc, ok := providerCfg.Extra["location"].(string); ok {
			logEntry = logEntry.Str("location", loc)
		}
		logEntry.Msg("initialized Vertex AI connector")
	}


	return nil
}

// initializeAzureProvider initializes Azure OpenAI connector
func (a *Adapter) initializeAzureProvider(cfg *config.Config) error {
		providerCfg := convertToProviderConfig(cfg.Providers.Azure, "AZURE_OPENAI_API_KEY")
		connector, err := connectors.NewConnector("azure", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Azure OpenAI connector: %w", err)
		}
		if err := a.registry.Register("azure-openai", connector); err != nil {
			return fmt.Errorf("failed to register Azure OpenAI connector: %w", err)
		}
		log.Info().Msg("initialized Azure OpenAI connector")
		return nil
}

// initializeMockProvider initializes a mock connector for development
func (a *Adapter) initializeMockProvider() error {
	log.Warn().Msg("no providers configured, using mock connector")
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
		Timeout: 30 * time.Second,
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	if err := a.registry.Register("mock", mockConnector); err != nil {
		return fmt.Errorf("failed to register mock connector: %w", err)
	}
	return nil
}

// performHealthChecks runs health checks on all registered providers
func (a *Adapter) performHealthChecks(ctx context.Context) {
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
}

// GetConnectorForModel returns the appropriate connector for a model ID
func (a *Adapter) GetConnectorForModel(modelID string) (connectors.Connector, string, error) {
	// Use the catalog to determine the actual provider for this model
	actualProvider := catalog.GetProviderForModel(modelID)
	if actualProvider == "" {
		// Fall back to extracting from model ID
		provider, _ := extractProviderFromModel(modelID)
		if provider != "" {
			actualProvider = provider
		} else {
			return nil, "", fmt.Errorf("unable to determine provider for model: %s", modelID)
		}
	}

	// Get the connector for the determined provider
	conn, err := a.registry.Get(actualProvider)
	if err != nil {
		return nil, "", fmt.Errorf("provider not available: %s", actualProvider)
	}
	
	// Return the full model ID (not just the model part)
	return conn, modelID, nil
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
	if apiKeyEnv == "GOOGLE_API_KEY" { //nolint:gosec // This is an environment variable name, not a hardcoded key
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
