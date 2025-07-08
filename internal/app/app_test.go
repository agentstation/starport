package app

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
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
}

func TestNewWithConfig(t *testing.T) {
	config := &Config{
		Server: server.Config{
			Port:            9090,
			ReadTimeout:     5 * time.Second,
			WriteTimeout:    5 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 15 * time.Second,
		},
	}

	app, err := New(WithConfig(config))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if app.config.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", app.config.Server.Port)
	}
}

func TestAppRun(t *testing.T) {
	// Create app with port 0 to use random port
	config := &Config{
		Server: server.Config{
			Port:            0,
			ShutdownTimeout: 5 * time.Second,
		},
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
			Port:            0,
			ShutdownTimeout: 5 * time.Second,
		},
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

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

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
			name: "no providers configured falls back to mock",
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
			}

			app, err := New(
				WithConfig(appConfig),
				WithProvidersConfig(tt.providersConfig),
			)
			if err != nil {
				t.Fatalf("failed to create app: %v", err)
			}

			// Check mock connector
			_, hasMock := app.connectorRegistry.Get("mock")
			if tt.expectMock && hasMock != nil {
				t.Error("expected mock connector to be registered")
			}
			if !tt.expectMock && hasMock == nil {
				t.Error("did not expect mock connector to be registered")
			}

			// Check expected providers
			for _, provider := range tt.expectProviders {
				if _, err := app.connectorRegistry.Get(provider); err != nil {
					t.Errorf("expected connector %s to be registered, but got error: %v", provider, err)
				}
			}
		})
	}
}