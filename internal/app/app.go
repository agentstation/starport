// Package app contains the core application logic for Starport.
package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"

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
	config      *Config
	httpServer  *server.Server
	hotReloader interface{ Stop() }
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

	// Initialize HTTP server
	app.httpServer = server.New(&app.config.Server)

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
