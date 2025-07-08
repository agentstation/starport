// Package app contains the core application logic for Starport.
package app

import (
	"context"
	"fmt"
)

// Config holds application configuration
type Config struct {
	// TODO: Add configuration fields
}

// App represents the main application
type App struct {
	config *Config
}

// Option is a functional option for App
type Option func(*App)

// WithConfig sets the app configuration
func WithConfig(cfg *Config) Option {
	return func(a *App) {
		a.config = cfg
	}
}

// New creates a new App instance
func New(opts ...Option) (*App, error) {
	app := &App{
		config: &Config{},
	}

	for _, opt := range opts {
		opt(app)
	}

	return app, nil
}

// Run starts the application
func (a *App) Run(ctx context.Context) error {
	fmt.Println("Starport server starting...")
	// TODO: Implement server logic
	<-ctx.Done()
	fmt.Println("Starport server shutting down...")
	return nil
}
