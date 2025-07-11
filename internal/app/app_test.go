package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/testutil"
)

func TestNew(t *testing.T) {
	// Test with default config
	app, err := New()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if app == nil {
		t.Fatal("expected app to be created")
	}

	if app.config == nil {
		t.Error("expected config to be initialized")
	}

	if app.httpServer == nil {
		t.Error("expected HTTP server to be initialized")
	}

	// Verify defaults
	if app.config.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", app.config.Server.Port)
	}
	if app.config.StorageMode != "badger" {
		t.Errorf("expected default storage mode 'badger', got %s", app.config.StorageMode)
	}
	if app.config.LogLevel != "info" {
		t.Errorf("expected default log level 'info', got %s", app.config.LogLevel)
	}
}

func TestDefaultConfig(t *testing.T) {
	// Test that DefaultConfig has expected values
	if DefaultConfig.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", DefaultConfig.Server.Port)
	}
	if DefaultConfig.StorageMode != "badger" {
		t.Errorf("expected default storage mode 'badger', got %s", DefaultConfig.StorageMode)
	}
	if DefaultConfig.LogLevel != "info" {
		t.Errorf("expected default log level 'info', got %s", DefaultConfig.LogLevel)
	}
}

func TestConfigApply(t *testing.T) {
	// Test that Apply creates a new config without modifying the original
	original := DefaultConfig
	modified := original.Apply(
		WithServerConfig(server.Config{Port: 9090}),
		WithStorageMode("valkey"),
		WithLogLevel("debug"),
	)

	// Original should be unchanged
	if original.Server.Port != 8080 {
		t.Errorf("original config modified: expected port 8080, got %d", original.Server.Port)
	}

	// Modified should have new values
	if modified.Server.Port != 9090 {
		t.Errorf("expected modified port 9090, got %d", modified.Server.Port)
	}
	if modified.StorageMode != "valkey" {
		t.Errorf("expected modified storage mode 'valkey', got %s", modified.StorageMode)
	}
	if modified.LogLevel != "debug" {
		t.Errorf("expected modified log level 'debug', got %s", modified.LogLevel)
	}
}

func TestNewWithConfig(t *testing.T) {
	// Test using individual options instead of WithConfig
	app, err := New(
		WithServerConfig(server.Config{
			Port:            9090,
			ReadTimeout:     5 * time.Second,
			WriteTimeout:    5 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 15 * time.Second,
		}),
		WithStorageMode("valkey"),
		WithLogLevel("debug"),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if app.config.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", app.config.Server.Port)
	}
	if app.config.StorageMode != "valkey" {
		t.Errorf("expected storage mode 'valkey', got %s", app.config.StorageMode)
	}
	if app.config.LogLevel != "debug" {
		t.Errorf("expected log level 'debug', got %s", app.config.LogLevel)
	}
}

func TestAppRun(t *testing.T) {
	// Use a valid port for testing
	config := &Config{
		Server: server.Config{
			Port:            18081,
			ShutdownTimeout: 5 * time.Second,
		},
		StorageMode: "badger",
		LogLevel:    "info",
	}

	app, err := New(WithConfig(config))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Run app
	err = app.Run(ctx)
	if err != nil {
		t.Errorf("expected no error on shutdown, got %v", err)
	}
}

func TestAppRunWithCancel(t *testing.T) {
	config := &Config{
		Server: server.Config{
			Port:            18080, // Use a specific port for testing
			ShutdownTimeout: 5 * time.Second,
		},
		StorageMode: "badger",
		LogLevel:    "info",
	}

	app, err := New(WithConfig(config))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Run app in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- app.Run(ctx)
	}()

	// Wait for server to be ready
	testutil.WaitForServer(t, fmt.Sprintf("http://localhost:%d/health/live", config.Server.Port), 2*time.Second)

	// Cancel context
	cancel()

	// Wait for shutdown
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("expected no error on shutdown, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("timeout waiting for app to shutdown")
	}
}

func TestApp_InitializeConnectors(t *testing.T) {
	tests := []struct {
		name            string
		providersConfig *config.ProvidersConfig
		expectMock      bool
		expectProviders []string
	}{
		{
			name:            "no providers config uses mock",
			providersConfig: nil,
			expectMock:      true,
			expectProviders: []string{},
		},
		{
			name: "single provider configured",
			providersConfig: &config.ProvidersConfig{
				OpenAI: config.ProviderConfig{
					BaseURL: "https://api.openai.com/v1",
					Timeout: 30 * time.Second,
				},
			},
			expectMock:      false,
			expectProviders: []string{"openai"},
		},
		{
			name: "multiple providers configured",
			providersConfig: &config.ProvidersConfig{
				OpenAI: config.ProviderConfig{
					BaseURL: "https://api.openai.com/v1",
					Timeout: 30 * time.Second,
				},
				Anthropic: config.ProviderConfig{
					BaseURL: "https://api.anthropic.com/v1",
					Timeout: 30 * time.Second,
				},
				Groq: config.ProviderConfig{
					BaseURL: "https://api.groq.com/openai/v1",
					Timeout: 30 * time.Second,
				},
			},
			expectMock:      false,
			expectProviders: []string{"openai", "anthropic", "groq"},
		},
		{
			name:            "no providers configured falls back to mock",
			providersConfig: &config.ProvidersConfig{
				// All providers have empty BaseURL
			},
			expectMock:      true,
			expectProviders: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appConfig := &Config{
				Server: server.Config{
					Port: 8080,
				},
				StorageMode: "badger",
				LogLevel:    "info",
				Providers:   tt.providersConfig,
			}

			app, err := New(WithConfig(appConfig))
			if err != nil {
				t.Fatalf("failed to create app: %v", err)
			}

			// Check mock connector
			mockConnector, _ := app.registry.Get("mock")
			if tt.expectMock && mockConnector == nil {
				t.Error("expected mock connector to be registered")
			}
			if !tt.expectMock && mockConnector != nil {
				t.Error("did not expect mock connector to be registered")
			}

			// Check expected providers
			for _, provider := range tt.expectProviders {
				connector, _ := app.registry.Get(provider)
				if connector == nil {
					t.Errorf("expected connector %s to be registered, but it was not found", provider)
				}
			}
		})
	}
}
