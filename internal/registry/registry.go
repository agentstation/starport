// Package registry manages LLM provider connectors
package registry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/pkg/catalog"
)

// Config contains configuration for the registry
type Config struct {
	// Provider configuration
	Providers *config.ProvidersConfig

	// Whether to perform health checks on initialization
	HealthCheckOnInit bool

	// Future: hot reload settings, retry config, etc.
}

// Registry manages provider connectors and their lifecycle
type Registry struct {
	connectors       map[string]connectors.Connector
	providerConfigs  map[string]connectors.ProviderConfig
	mu               sync.RWMutex
	config           *Config
}

// providerInit holds initialization data for a provider
type providerInit struct {
	name      string
	baseURL   string
	apiKeyEnv string
}

// New creates and initializes a new connector registry
func New(cfg *Config) (*Registry, error) {
	r := &Registry{
		connectors:      make(map[string]connectors.Connector),
		providerConfigs: make(map[string]connectors.ProviderConfig),
		config:          cfg,
	}

	// Initialize providers from configuration
	ctx := context.Background()
	if err := r.initializeFromConfig(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize registry: %w", err)
	}

	// Optionally perform health checks
	if cfg != nil && cfg.HealthCheckOnInit {
		r.performHealthChecks(ctx)
	}

	return r, nil
}

// NewEmpty creates an empty registry without initialization
// Useful for testing
func NewEmpty() *Registry {
	return &Registry{
		connectors:      make(map[string]connectors.Connector),
		providerConfigs: make(map[string]connectors.ProviderConfig),
	}
}

// initializeFromConfig initializes connectors based on configuration
func (r *Registry) initializeFromConfig(_ context.Context) error {
	if r.config == nil || r.config.Providers == nil {
		// No providers configured, use mock connector
		return r.initializeMockProvider()
	}

	cfg := &config.Config{
		Providers: *r.config.Providers,
	}

	// Define provider configurations
	providers := []providerInit{
		{"openai", cfg.Providers.OpenAI.BaseURL, "OPENAI_API_KEY"},
		{"anthropic", cfg.Providers.Anthropic.BaseURL, "ANTHROPIC_API_KEY"},
		{"groq", cfg.Providers.Groq.BaseURL, "GROQ_API_KEY"},
		{"mistral", cfg.Providers.Mistral.BaseURL, "MISTRAL_API_KEY"},
	}

	// Initialize standard providers
	for _, p := range providers {
		if err := r.initializeProvider(p, cfg); err != nil {
			return err
		}
	}

	// Handle Google providers separately due to special logic
	if err := r.initializeGoogleProviders(cfg); err != nil {
		return err
	}

	// Initialize Azure OpenAI if configured
	if cfg.Providers.Azure.BaseURL != "" {
		if err := r.initializeAzureProvider(cfg); err != nil {
			return err
		}
	}

	// Initialize Ollama if enabled
	if cfg.Providers.Ollama.Enabled {
		if err := r.initializeOllamaProvider(cfg); err != nil {
			log.Warn().Err(err).Msg("failed to initialize Ollama connector - continuing without Ollama support")
			// Don't fail startup if Ollama is not available
		}
	}

	// If no providers are configured, use mock connector for development
	if len(r.ListProviders()) == 0 {
		if err := r.initializeMockProvider(); err != nil {
			return err
		}
	}

	return nil
}

// initializeProvider initializes a standard provider
func (r *Registry) initializeProvider(p providerInit, cfg *config.Config) error {
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
	if err := r.RegisterWithConfig(p.name, connector, providerCfg); err != nil {
		return fmt.Errorf("failed to register %s connector: %w", p.name, err)
	}
	log.Info().Msgf("initialized %s connector", p.name)
	return nil
}

// initializeGoogleProviders handles Google AI Studio and Vertex AI initialization
func (r *Registry) initializeGoogleProviders(cfg *config.Config) error {
	// Initialize Google AI Studio if configured
	if cfg.Providers.GoogleAIStudio.BaseURL != "" {
		providerCfg := convertToProviderConfig(cfg.Providers.GoogleAIStudio, "GOOGLE_API_KEY")
		connector, err := connectors.NewConnector("google-ai-studio", providerCfg)
		if err != nil {
			return fmt.Errorf("failed to create Google AI Studio connector: %w", err)
		}
		if err := r.RegisterWithConfig("google-ai-studio", connector, providerCfg); err != nil {
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
		if err := r.RegisterWithConfig("google-ai-studio", connector, providerCfg); err != nil {
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
		if err := r.RegisterWithConfig("google-vertex", connector, providerCfg); err != nil {
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
func (r *Registry) initializeAzureProvider(cfg *config.Config) error {
	providerCfg := convertToProviderConfig(cfg.Providers.Azure, "AZURE_OPENAI_API_KEY")
	connector, err := connectors.NewConnector("azure", providerCfg)
	if err != nil {
		return fmt.Errorf("failed to create Azure OpenAI connector: %w", err)
	}
	if err := r.RegisterWithConfig("azure-openai", connector, providerCfg); err != nil {
		return fmt.Errorf("failed to register Azure OpenAI connector: %w", err)
	}
	log.Info().Msg("initialized Azure OpenAI connector")
	return nil
}

// initializeOllamaProvider initializes Ollama connector
func (r *Registry) initializeOllamaProvider(cfg *config.Config) error {
	providerCfg := convertToProviderConfig(cfg.Providers.Ollama, "")
	providerCfg.Enabled = true // Ensure enabled flag is set
	
	connector, err := connectors.NewConnector("ollama", providerCfg)
	if err != nil {
		return fmt.Errorf("failed to create Ollama connector: %w", err)
	}
	
	// Check if Ollama is actually running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := connector.Health(ctx); err != nil {
		return fmt.Errorf("ollama server not reachable at %s: %w", providerCfg.BaseURL, err)
	}
	
	if err := r.RegisterWithConfig("ollama", connector, providerCfg); err != nil {
		return fmt.Errorf("failed to register Ollama connector: %w", err)
	}
	
	// Fetch available models from Ollama and register them
	modelsResp, err := connector.Models(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("failed to fetch Ollama models during initialization")
	} else if modelsResp != nil && modelsResp.Data != nil {
		// Register Ollama models with the catalog
		for _, model := range modelsResp.Data {
			modelInfo := catalog.DynamicModelInfo{
				ID:      model.ID,
				Created: model.Created,
				OwnedBy: model.OwnedBy,
			}
			if err := catalog.RegisterDynamicModel("ollama", modelInfo); err != nil {
				log.Warn().
					Err(err).
					Str("model", model.ID).
					Msg("failed to register Ollama model")
			}
		}
		log.Info().
			Int("count", len(modelsResp.Data)).
			Str("base_url", providerCfg.BaseURL).
			Msg("initialized Ollama connector and registered models")
	} else {
		log.Info().Str("base_url", providerCfg.BaseURL).Msg("initialized Ollama connector (no models available)")
	}
	
	return nil
}

// initializeMockProvider initializes a mock connector for development
func (r *Registry) initializeMockProvider() error {
	log.Warn().Msg("no providers configured, using mock connector")
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
		Timeout: 30 * time.Second,
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	if err := r.RegisterWithConfig("mock", mockConnector, mockConfig); err != nil {
		return fmt.Errorf("failed to register mock connector: %w", err)
	}
	return nil
}

// performHealthChecks runs health checks on configured providers concurrently
func (r *Registry) performHealthChecks(ctx context.Context) {
	r.mu.RLock()
	
	// Count configured providers
	configuredCount := 0
	for provider := range r.connectors {
		if r.IsProviderConfigured(provider) {
			configuredCount++
		}
	}
	
	if configuredCount == 0 {
		r.mu.RUnlock()
		log.Info().Msg("no configured providers to health check")
		return
	}
	
	log.Info().
		Int("providers", configuredCount).
		Msg("starting health checks for configured providers")
	
	// Use WaitGroup to track health checks
	var wg sync.WaitGroup

	for provider, connector := range r.connectors {
		// Only health check configured providers
		if !r.IsProviderConfigured(provider) {
			continue
		}

		// Launch health check in goroutine
		wg.Add(1)
		go func(p string, c connectors.Connector) {
			defer wg.Done()

			// Run health check with timeout
			checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			start := time.Now()
			err := c.Health(checkCtx)
			duration := time.Since(start)

			// Log result immediately
			if err != nil {
				log.Warn().
					Err(err).
					Str("provider", p).
					Dur("duration", duration).
					Msg("provider health check failed")
			} else {
				log.Info().
					Str("provider", p).
					Dur("duration", duration).
					Msg("provider health check passed")
			}
		}(provider, connector)
	}
	
	r.mu.RUnlock()

	// Wait for all health checks to complete
	wg.Wait()
	
	log.Info().Msg("all health checks completed")
}

// GetConnectorForModel returns the appropriate connector for a model ID
func (r *Registry) GetConnectorForModel(modelID string) (connectors.Connector, string, error) {
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
	conn, err := r.Get(actualProvider)
	if err != nil {
		return nil, "", fmt.Errorf("provider not available: %s", actualProvider)
	}
	
	// Return the full model ID (not just the model part)
	return conn, modelID, nil
}

// Register adds a connector to the registry
func (r *Registry) Register(provider string, connector connectors.Connector) error {
	// For backward compatibility, register with empty config
	return r.RegisterWithConfig(provider, connector, connectors.ProviderConfig{})
}

// RegisterWithConfig adds a connector to the registry with its configuration
func (r *Registry) RegisterWithConfig(provider string, connector connectors.Connector, config connectors.ProviderConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.connectors[provider]; exists {
		return fmt.Errorf("connector already registered for provider: %s", provider)
	}

	r.connectors[provider] = connector
	r.providerConfigs[provider] = config
	log.Info().
		Str("provider", provider).
		Msg("registered connector")

	// Validate models for this provider (except Ollama which handles its own)
	if provider != "ollama" && provider != "mock" {
		go r.validateProviderModels(provider, connector)
	}

	return nil
}

// Get retrieves a connector by provider name
func (r *Registry) Get(provider string) (connectors.Connector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	connector, exists := r.connectors[provider]
	if !exists {
		return nil, fmt.Errorf("no connector registered for provider: %s", provider)
	}

	return connector, nil
}

// GetAll returns all registered connectors
func (r *Registry) GetAll() map[string]connectors.Connector {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent concurrent modification
	result := make(map[string]connectors.Connector, len(r.connectors))
	for k, v := range r.connectors {
		result[k] = v
	}

	return result
}

// ListProviders returns a list of registered provider names
func (r *Registry) ListProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]string, 0, len(r.connectors))
	for provider := range r.connectors {
		providers = append(providers, provider)
	}

	return providers
}

// HasProvider checks if a provider is registered
func (r *Registry) HasProvider(provider string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.connectors[provider]
	return exists
}

// IsProviderConfigured checks if a provider has an API key configured
func (r *Registry) IsProviderConfigured(provider string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	config, exists := r.providerConfigs[provider]
	if !exists {
		return false
	}

	// Check if the provider has an API key
	// Special handling for providers that don't need an API key
	if provider == "mock" || provider == "ollama" {
		return true
	}

	// For Azure OpenAI, check both API key and valid resource URL
	if provider == "azure-openai" {
		hasAPIKey := config.APIKey != ""
		hasValidURL := config.BaseURL != "" && 
			!strings.Contains(config.BaseURL, "YOUR-RESOURCE-NAME") &&
			!strings.Contains(config.BaseURL, "your-resource-name")
		return hasAPIKey && hasValidURL
	}

	// For Vertex AI, check for project ID instead of API key
	if provider == "google-vertex" && config.Extra != nil {
		_, hasProjectID := config.Extra["project_id"]
		return hasProjectID
	}

	// For all other providers, check for API key
	return config.APIKey != ""
}

// Close closes all connectors
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error

	for provider, connector := range r.connectors {
		if err := connector.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close %s connector: %w", provider, err))
		}
	}

	// Clear the map
	r.connectors = make(map[string]connectors.Connector)

	if len(errs) > 0 {
		return fmt.Errorf("errors closing connectors: %v", errs)
	}

	return nil
}

// HealthCheck performs health checks on configured connectors concurrently
func (r *Registry) HealthCheck(ctx context.Context) map[string]error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Create a map to store results with mutex for thread safety
	results := make(map[string]error)
	resultsMu := &sync.Mutex{}

	// Use WaitGroup to wait for all health checks to complete
	var wg sync.WaitGroup

	for provider, connector := range r.connectors {
		// Only health check configured providers
		if !r.IsProviderConfigured(provider) {
			continue
		}

		// Launch health check in goroutine
		wg.Add(1)
		go func(p string, c connectors.Connector) {
			defer wg.Done()

			// Run health check with timeout
			checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			err := c.Health(checkCtx)

			// Store result thread-safely
			resultsMu.Lock()
			results[p] = err
			resultsMu.Unlock()
		}(provider, connector)
	}

	// Wait for all health checks to complete
	wg.Wait()

	return results
}

// GetModels returns all available models from all providers
func (r *Registry) GetModels(ctx context.Context) ([]connectors.Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get catalog
	_, err := catalog.GetCatalog()
	if err != nil {
		// Fall back to dynamic fetching if catalog fails
		log.Warn().
			Err(err).
			Msg("failed to load catalog, falling back to dynamic model fetching")
		
		var allModels []connectors.Model
		for provider, connector := range r.connectors {
			// Skip providers without API keys
			if !r.IsProviderConfigured(provider) {
				log.Debug().
					Str("provider", provider).
					Msg("skipping provider without API key")
				continue
			}

			modelsResp, err := connector.Models(ctx)
			if err != nil {
				// Log error but continue with other providers
				log.Warn().
					Err(err).
					Str("provider", provider).
					Msg("failed to get models from provider")
				continue
			}

			if modelsResp != nil && modelsResp.Data != nil {
				allModels = append(allModels, modelsResp.Data...)
			}
		}
		return allModels, nil
	}

	// Filter catalog models to only include those from registered and configured providers
	var allModels []connectors.Model
	for provider := range r.connectors {
		// Skip providers without API keys
		if !r.IsProviderConfigured(provider) {
			log.Debug().
				Str("provider", provider).
				Msg("skipping provider without API key")
			continue
		}

		// Use the new function to get both static and dynamic models
		catalogModels := catalog.GetModelsByProviderWithDynamic(provider)
		for _, catalogModel := range catalogModels {
			// Convert catalog model to connectors.Model
			model := connectors.Model{
				ID:      catalogModel.ID,
				Object:  "model",
				Created: catalogModel.Created,
				OwnedBy: provider,
			}
			allModels = append(allModels, model)
		}
	}

	// If no models found in catalog, try dynamic fetching
	if len(allModels) == 0 {
		log.Warn().Msg("no models found in catalog, trying dynamic fetching")
		for provider, connector := range r.connectors {
			// Skip providers without API keys
			if !r.IsProviderConfigured(provider) {
				log.Debug().
					Str("provider", provider).
					Msg("skipping provider without API key")
				continue
			}

			modelsResp, err := connector.Models(ctx)
			if err != nil {
				// Log error but continue with other providers
				log.Warn().
					Err(err).
					Str("provider", provider).
					Msg("failed to get models from provider")
				continue
			}

			if modelsResp != nil && modelsResp.Data != nil {
				allModels = append(allModels, modelsResp.Data...)
			}
		}
	}

	return allModels, nil
}

// GetProviderMetadata returns metadata for all registered providers
func (r *Registry) GetProviderMetadata() []connectors.ProviderMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get all provider metadata
	allMetadata := connectors.GetProviderMetadata()

	// Filter to only include registered providers
	var registeredMetadata []connectors.ProviderMetadata
	for _, meta := range allMetadata {
		if _, exists := r.connectors[meta.Slug]; exists {
			registeredMetadata = append(registeredMetadata, meta)
		}
	}

	return registeredMetadata
}

// validateProviderModels fetches actual models from the provider and updates the catalog
func (r *Registry) validateProviderModels(provider string, connector connectors.Connector) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Info().
		Str("provider", provider).
		Msg("starting model validation for provider")

	// Fetch actual models from the provider
	modelsResp, err := connector.Models(ctx)
	if err != nil {
		log.Warn().
			Err(err).
			Str("provider", provider).
			Msg("failed to fetch models from provider during validation")
		return
	}

	if modelsResp == nil || modelsResp.Data == nil {
		log.Warn().
			Str("provider", provider).
			Msg("no models returned from provider")
		return
	}

	// Get catalog models for this provider
	catalogModels := catalog.GetModelsByProviderWithDynamic(provider)
	catalogModelMap := make(map[string]bool)
	for _, model := range catalogModels {
		catalogModelMap[model.ID] = true
	}

	// Check which provider models exist in catalog
	providerModelMap := make(map[string]bool)
	newModels := 0
	for _, model := range modelsResp.Data {
		providerModelMap[model.ID] = true
		
		// If model not in catalog, register it as dynamic
		if !catalogModelMap[model.ID] {
			modelInfo := catalog.DynamicModelInfo{
				ID:      model.ID,
				Created: model.Created,
				OwnedBy: model.OwnedBy,
			}
			if err := catalog.RegisterDynamicModel(provider, modelInfo); err != nil {
				log.Warn().
					Err(err).
					Str("provider", provider).
					Str("model", model.ID).
					Msg("failed to register new model")
			} else {
				newModels++
				log.Debug().
					Str("provider", provider).
					Str("model", model.ID).
					Msg("registered new model not found in catalog")
			}
		}
	}

	// Find catalog models that don't exist in provider
	missingModels := 0
	for _, model := range catalogModels {
		if !providerModelMap[model.ID] {
			missingModels++
			log.Warn().
				Str("provider", provider).
				Str("model", model.ID).
				Msg("catalog model not found in provider API")
		}
	}

	log.Info().
		Str("provider", provider).
		Int("provider_models", len(modelsResp.Data)).
		Int("catalog_models", len(catalogModels)).
		Int("new_models", newModels).
		Int("missing_models", missingModels).
		Msg("completed model validation for provider")
}

// Helper functions

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
		Enabled:           cfg.Enabled,
	}
}