// Package app contains the core application logic for Starport.
package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/chatui"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
)

// App represents the main application with new handler structure
type App struct {
	config       *Config
	httpServer   *server.Server
	hotReloader  interface {
		Start(context.Context) error
		Stop()
	}
	registry     *registry.Registry
	store        storage.KVStore
	cacheManager *cache.Manager
}

// New creates a new App instance with improved handler organization
func New(opts ...Option) (*App, error) {
	// Apply options to default config
	cfg := DefaultConfig.Apply(opts...)

	app := &App{
		config: &cfg,
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize storage
	store, err := app.initializeStorage()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}
	app.store = store

	// Initialize hot reloader if configured
	if cfg.HotReload != nil && cfg.HotReload.Enabled {
		hotReloader, err := config.NewHotReloader(
			cfg.HotReload.ConfigPath,
			cfg.HotReload.CheckInterval,
		)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to initialize rate limit hot reloader")
		} else {
			app.hotReloader = hotReloader
			log.Info().
				Str("config_path", cfg.HotReload.ConfigPath).
				Msg("Rate limit hot reload configured")
		}
	}

	// Initialize connector registry with providers
	registryCfg := &registry.Config{
		Providers:         cfg.Providers,
		HealthCheckOnInit: true,
	}
	app.registry, err = registry.New(registryCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize registry: %w", err)
	}

	// Initialize cache manager if caching is enabled
	if cfg.EnableCache {
		cacheConfig := cache.ManagerConfig{
			// Use default cache configuration
		}
		cacheManager, err := cache.NewCacheManager(cacheConfig, app.store)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to initialize cache manager")
		} else {
			app.cacheManager = cacheManager
			log.Info().Msg("Cache manager initialized")
		}
	}

	// Initialize HTTP server with new structure
	serverOpts := []server.Option{}
	if app.cacheManager != nil {
		serverOpts = append(serverOpts, server.WithCache(app.cacheManager))
	}
	
	// Add ChatUI configuration if enabled
	if cfg.ChatUI != nil && cfg.ChatUI.Enabled {
		chatUIConfig := chatui.Config{
			Title:       cfg.ChatUI.Title,
			Theme:       cfg.ChatUI.Theme,
			AllowKeyGen: cfg.ChatUI.AllowKeyGen,
			APIBaseURL:  fmt.Sprintf("http://localhost:%d", cfg.Server.Port),
		}
		serverOpts = append(serverOpts, server.WithChatUI(&chatUIConfig))
		log.Info().
			Str("title", cfg.ChatUI.Title).
			Str("theme", cfg.ChatUI.Theme).
			Bool("allow_key_gen", cfg.ChatUI.AllowKeyGen).
			Msg("ChatUI enabled")
	}
	
	app.httpServer = server.New(&app.config.Server, app.registry, serverOpts...)

	return app, nil
}

// initializeStorage initializes the storage backend based on configuration
func (a *App) initializeStorage() (storage.KVStore, error) {
	switch a.config.StorageMode {
	case "badger":
		// TODO: Initialize Badger storage when implemented
		log.Warn().Msg("Badger storage not yet implemented, using mock storage")
		return storage.NewMockStore(), nil

	case "valkey":
		if a.config.Storage == nil {
			return nil, fmt.Errorf("valkey storage configuration not provided")
		}
		
		// Convert config.ValkeyConfig to storage.ValkeyConfig
		valkeyConfig := storage.ValkeyConfig{
			URL:          a.config.Storage.Valkey.URL,
			Password:     a.config.Storage.Valkey.Password,
			DB:           0, // Default DB
			MaxRetries:   3,
			MinIdleConns: a.config.Storage.Valkey.MinIdleConns,
			ReadTimeout:  a.config.Storage.Valkey.ReadTimeout,
			WriteTimeout: a.config.Storage.Valkey.WriteTimeout,
			ClusterMode:  a.config.Storage.Valkey.ClusterMode,
		}

		store, err := storage.OpenValkey(valkeyConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to open valkey storage: %w", err)
		}

		log.Info().
			Str("url", valkeyConfig.URL).
			Bool("cluster", valkeyConfig.ClusterMode).
			Msg("initialized valkey storage")

		return store, nil

	default:
		return nil, fmt.Errorf("unknown storage mode: %s", a.config.StorageMode)
	}
}


// Run starts the application
func (a *App) Run(ctx context.Context) error {
	log.Info().
		Str("storage_mode", a.config.StorageMode).
		Str("log_level", a.config.LogLevel).
		Int("port", a.config.Server.Port).
		Msg("starting starport application")

	// Start hot reloader if configured
	if a.hotReloader != nil {
		if err := a.hotReloader.Start(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to start rate limit hot reloader")
		} else {
			log.Info().Msg("Rate limit hot reload started")
		}
	}

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

		// Create shutdown context with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Shutdown HTTP server
		if err := a.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shutdown HTTP server: %w", err)
		}

		// Wait for goroutines to finish
		wg.Wait()

		// Close all connectors
		if err := a.registry.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close connectors")
		}

		// Close cache manager
		if a.cacheManager != nil {
			if err := a.cacheManager.Close(); err != nil {
				log.Error().Err(err).Msg("failed to close cache manager")
			}
		}

		// Close storage
		if err := a.store.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close storage")
		}

		log.Info().Msg("Starport application stopped successfully")
		return nil

	case err := <-errChan:
		// Server encountered an error
		return err
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Add validation logic here
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Server.Port)
	}
	if c.StorageMode != "badger" && c.StorageMode != "valkey" {
		return fmt.Errorf("invalid storage mode: %s", c.StorageMode)
	}

	// Validate hot reload config
	if c.HotReload != nil && c.HotReload.Enabled {
		if c.HotReload.ConfigPath == "" {
			return fmt.Errorf("hot reload config path cannot be empty when enabled")
		}
		if c.HotReload.CheckInterval <= 0 {
			return fmt.Errorf("hot reload check interval must be positive")
		}
	}

	return nil
}
