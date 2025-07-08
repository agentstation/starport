// Package app contains the core application logic for Starport.
package app

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/connectors"
	"github.com/agentstation/starport/internal/server"
)

// Config holds application configuration
type Config struct {
	// Server configuration
	Server server.Config
	// Storage mode (badger or valkey)
	StorageMode string
	// Log level
	LogLevel string
}

// App represents the main application
type App struct {
	config            *Config
	httpServer        *server.Server
	hotReloader       interface{ Stop() }
	connectorRegistry *server.ConnectorRegistry
	providersConfig   *config.ProvidersConfig
}

// Option is a functional option for App
type Option func(*App)

// WithConfig sets the app configuration
func WithConfig(cfg *Config) Option {
	return func(a *App) {
		a.config = cfg
	}
}

// WithHotReloader sets the hot reloader for dynamic config updates
func WithHotReloader(hr interface{ Stop() }) Option {
	return func(a *App) {
		a.hotReloader = hr
	}
}

// WithProvidersConfig sets the providers configuration
func WithProvidersConfig(cfg *config.ProvidersConfig) Option {
	return func(a *App) {
		a.providersConfig = cfg
	}
}

// New creates a new App instance
func New(opts ...Option) (*App, error) {
	app := &App{
		config: &Config{
			Server: server.Config{
				Port: 8080,
			},
		},
	}

	for _, opt := range opts {
		opt(app)
	}

	// Initialize connector registry
	app.connectorRegistry = server.NewConnectorRegistry()

	// Initialize connectors based on configuration
	if err := app.initializeConnectors(); err != nil {
		return nil, fmt.Errorf("failed to initialize connectors: %w", err)
	}

	// Initialize HTTP server with registry
	app.httpServer = server.New(&app.config.Server, app.connectorRegistry)

	return app, nil
}

// Run starts the application
func (a *App) Run(ctx context.Context) error {
	log.Info().
		Str("storage_mode", a.config.StorageMode).
		Str("log_level", a.config.LogLevel).
		Int("port", a.config.Server.Port).
		Msg("starting Starport application")

	// Create error channel for server errors
	errChan := make(chan error, 1)

	// Start HTTP server in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := a.httpServer.Start(); err != nil {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		log.Info().Msg("shutting down Starport application")
		
		// Stop hot reloader if present
		if a.hotReloader != nil {
			a.hotReloader.Stop()
		}
		
		// Shutdown HTTP server
		if err := a.httpServer.Shutdown(context.Background()); err != nil {
			return fmt.Errorf("failed to shutdown HTTP server: %w", err)
		}
		
		// Wait for goroutines to finish
		wg.Wait()
		
		return nil
		
	case err := <-errChan:
		return err
	}
}

// initializeConnectors initializes all configured LLM provider connectors
func (a *App) initializeConnectors() error {
	// If no providers config, use a mock connector for development
	if a.providersConfig == nil {
		log.Warn().Msg("No providers configured, using mock connector")
		mockConfig := connectors.ProviderConfig{
			BaseURL: "http://mock",
			Timeout: 30 * time.Second,
		}
		mockConnector := connectors.NewMockConnector(mockConfig)
		a.connectorRegistry.Register("mock", mockConnector)
		return nil
	}

	// Initialize each configured provider
	initialized := 0

	// OpenAI
	if a.providersConfig.OpenAI.BaseURL != "" {
		cfg := convertToConnectorConfig(a.providersConfig.OpenAI, "OPENAI_API_KEY")
		connector, err := connectors.NewOpenAIConnector(cfg)
		if err != nil {
			log.Error().Err(err).Msg("failed to initialize OpenAI connector")
		} else {
			a.connectorRegistry.Register("openai", connector)
			initialized++
			log.Info().Str("provider", "openai").Msg("initialized connector")
		}
	}

	// Anthropic
	if a.providersConfig.Anthropic.BaseURL != "" {
		cfg := convertToConnectorConfig(a.providersConfig.Anthropic, "ANTHROPIC_API_KEY")
		connector, err := connectors.NewAnthropicConnector(cfg)
		if err != nil {
			log.Error().Err(err).Msg("failed to initialize Anthropic connector")
		} else {
			a.connectorRegistry.Register("anthropic", connector)
			initialized++
			log.Info().Str("provider", "anthropic").Msg("initialized connector")
		}
	}

	// Gemini
	if a.providersConfig.Gemini.BaseURL != "" {
		cfg := convertToConnectorConfig(a.providersConfig.Gemini, "GOOGLE_API_KEY")
		connector, err := connectors.NewGeminiConnector(cfg)
		if err != nil {
			log.Error().Err(err).Msg("failed to initialize Gemini connector")
		} else {
			a.connectorRegistry.Register("gemini", connector)
			initialized++
			log.Info().Str("provider", "gemini").Msg("initialized connector")
		}
	}

	// Groq
	if a.providersConfig.Groq.BaseURL != "" {
		cfg := convertToConnectorConfig(a.providersConfig.Groq, "GROQ_API_KEY")
		connector, err := connectors.NewGroqConnector(cfg)
		if err != nil {
			log.Error().Err(err).Msg("failed to initialize Groq connector")
		} else {
			a.connectorRegistry.Register("groq", connector)
			initialized++
			log.Info().Str("provider", "groq").Msg("initialized connector")
		}
	}

	// Mistral
	if a.providersConfig.Mistral.BaseURL != "" {
		cfg := convertToConnectorConfig(a.providersConfig.Mistral, "MISTRAL_API_KEY")
		connector, err := connectors.NewMistralConnector(cfg)
		if err != nil {
			log.Error().Err(err).Msg("failed to initialize Mistral connector")
		} else {
			a.connectorRegistry.Register("mistral", connector)
			initialized++
			log.Info().Str("provider", "mistral").Msg("initialized connector")
		}
	}

	// Azure OpenAI
	if a.providersConfig.Azure.BaseURL != "" {
		cfg := convertToConnectorConfig(a.providersConfig.Azure, "AZURE_OPENAI_API_KEY")
		connector, err := connectors.NewAzureOpenAIConnector(cfg)
		if err != nil {
			log.Error().Err(err).Msg("failed to initialize Azure OpenAI connector")
		} else {
			a.connectorRegistry.Register("azure", connector)
			initialized++
			log.Info().Str("provider", "azure").Msg("initialized connector")
		}
	}

	if initialized == 0 {
		// If no providers were initialized, add a mock connector
		log.Warn().Msg("No providers initialized, adding mock connector")
		mockConfig := connectors.ProviderConfig{
			BaseURL: "http://mock",
			Timeout: 30 * time.Second,
		}
		mockConnector := connectors.NewMockConnector(mockConfig)
		a.connectorRegistry.Register("mock", mockConnector)
	}

	log.Info().Int("count", initialized).Msg("initialized provider connectors")
	return nil
}

// convertToConnectorConfig converts from config.ProviderConfig to connectors.ProviderConfig
func convertToConnectorConfig(cfg config.ProviderConfig, apiKeyEnvVar string) connectors.ProviderConfig {
	return connectors.ProviderConfig{
		BaseURL:           cfg.BaseURL,
		Timeout:           cfg.Timeout,
		MaxConnections:    cfg.MaxConnections,
		MaxRetries:        cfg.MaxRetries,
		RetryDelay:        cfg.RetryDelay,
		BackoffMultiplier: cfg.BackoffMultiplier,
		APIKey:            os.Getenv(apiKeyEnvVar),
	}
}
