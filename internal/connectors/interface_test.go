package connectors_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/connectors"
)

func TestNewConnector(t *testing.T) {
	t.Run("Create mock connector", func(t *testing.T) {
		config := connectors.ProviderConfig{
			BaseURL: "http://mock.api",
			APIKey:  "test-key",
		}

		connector, err := connectors.NewConnector("mock", config)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if connector.Name() != "mock" {
			t.Errorf("expected name 'mock', got %s", connector.Name())
		}
	})

	t.Run("Create with unknown provider", func(t *testing.T) {
		config := connectors.ProviderConfig{
			BaseURL: "http://unknown.api",
		}

		_, err := connectors.NewConnector("unknown", config)
		if !errors.Is(err, connectors.ErrProviderNotSupported) {
			t.Errorf("expected ErrProviderNotSupported, got %v", err)
		}
	})

	t.Run("Create Gemini connector", func(t *testing.T) {
		config := connectors.ProviderConfig{
			APIKey:  "test-key",
		}

		connector, err := connectors.NewConnector("gemini", config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if connector.Name() != "gemini" {
			t.Errorf("expected name 'gemini', got %s", connector.Name())
		}
	})

	t.Run("Create Groq connector", func(t *testing.T) {
		config := connectors.ProviderConfig{
			APIKey:  "test-key",
		}

		connector, err := connectors.NewConnector("groq", config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if connector.Name() != "groq" {
			t.Errorf("expected name 'groq', got %s", connector.Name())
		}
	})

	t.Run("Create Mistral connector", func(t *testing.T) {
		config := connectors.ProviderConfig{
			APIKey:  "test-key",
		}

		connector, err := connectors.NewConnector("mistral", config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if connector.Name() != "mistral" {
			t.Errorf("expected name 'mistral', got %s", connector.Name())
		}
	})

	t.Run("Create Azure OpenAI connector", func(t *testing.T) {
		config := connectors.ProviderConfig{
			BaseURL: "https://myresource.openai.azure.com",
			APIKey:  "test-key",
		}

		connector, err := connectors.NewConnector("azure", config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if connector.Name() != "azure" {
			t.Errorf("expected name 'azure', got %s", connector.Name())
		}
	})

	t.Run("Create OpenAI connector", func(t *testing.T) {
		config := connectors.ProviderConfig{
			APIKey:  "test-key",
		}

		connector, err := connectors.NewConnector("openai", config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if connector.Name() != "openai" {
			t.Errorf("expected name 'openai', got %s", connector.Name())
		}
	})

	t.Run("Create Anthropic connector", func(t *testing.T) {
		config := connectors.ProviderConfig{
			APIKey:  "test-key",
		}

		connector, err := connectors.NewConnector("anthropic", config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if connector.Name() != "anthropic" {
			t.Errorf("expected name 'anthropic', got %s", connector.Name())
		}
	})
}

func TestProviderConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  connectors.ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: connectors.ProviderConfig{
				BaseURL:           "https://api.example.com",
				Timeout:           30 * time.Second,
				MaxConnections:    100,
				MaxRetries:        3,
				RetryDelay:        1 * time.Second,
				BackoffMultiplier: 2.0,
			},
			wantErr: false,
		},
		{
			name: "missing base URL",
			config: connectors.ProviderConfig{
				Timeout: 30 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "defaults applied",
			config: connectors.ProviderConfig{
				BaseURL: "https://api.example.com",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr && err == nil {
				// Check defaults were applied
				if tt.config.Timeout <= 0 {
					t.Error("expected positive timeout")
				}
				if tt.config.MaxConnections <= 0 {
					t.Error("expected positive max connections")
				}
				if tt.config.BackoffMultiplier <= 1 {
					t.Error("expected backoff multiplier > 1")
				}
			}
		})
	}
}

func TestHealthStatus(t *testing.T) {
	status := connectors.HealthStatus{
		Healthy:   true,
		Latency:   10 * time.Millisecond,
		CheckedAt: time.Now(),
	}

	if !status.Healthy {
		t.Error("expected healthy status")
	}

	errorStatus := connectors.HealthStatus{
		Healthy:   false,
		Latency:   5 * time.Second,
		Error:     "connection timeout",
		CheckedAt: time.Now(),
	}

	if errorStatus.Healthy {
		t.Error("expected unhealthy status")
	}
	if errorStatus.Error == "" {
		t.Error("expected error message")
	}
}

func TestConnectorInterface(t *testing.T) {
	// Test that MockConnector implements Connector interface
	var _ connectors.Connector = (*connectors.MockConnector)(nil)

	// Create mock connector
	config := connectors.ProviderConfig{
		BaseURL: "http://mock.api",
		APIKey:  "test-key",
	}
	mock := connectors.NewMockConnector(config)
	defer mock.Close()

	ctx := context.Background()

	t.Run("Chat", func(t *testing.T) {
		req := &connectors.ChatRequest{
			Model: "mock-model",
			Messages: []connectors.Message{
				{
					Role:    "user",
					Content: "Hello",
				},
			},
		}

		resp, err := mock.Chat(ctx, req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if resp.Model != "mock-model" {
			t.Errorf("expected model 'mock-model', got %s", resp.Model)
		}
		if len(resp.Choices) != 1 {
			t.Errorf("expected 1 choice, got %d", len(resp.Choices))
		}
	})

	t.Run("ChatStream", func(t *testing.T) {
		req := &connectors.ChatRequest{
			Model:  "mock-model",
			Stream: true,
			Messages: []connectors.Message{
				{
					Role:    "user",
					Content: "Hello",
				},
			},
		}

		stream, err := mock.ChatStream(ctx, req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer stream.Close()

		chunks := 0
		for {
			chunk, err := stream.Recv()
			if err != nil {
				if errors.Is(err, errors.New("EOF")) {
					break
				}
				// Check for io.EOF manually since we can't import io in test
				if err.Error() == "EOF" {
					break
				}
				t.Fatalf("unexpected error: %v", err)
			}
			chunks++
			if chunk.Model != "mock-model" {
				t.Errorf("expected model 'mock-model', got %s", chunk.Model)
			}
		}

		if chunks != 3 {
			t.Errorf("expected 3 chunks, got %d", chunks)
		}
	})

	t.Run("Embeddings", func(t *testing.T) {
		req := &connectors.EmbeddingsRequest{
			Model: "mock-embedding-model",
			Input: "test input",
		}

		resp, err := mock.Embeddings(ctx, req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if resp.Model != "mock-embedding-model" {
			t.Errorf("expected model 'mock-embedding-model', got %s", resp.Model)
		}
		if len(resp.Data) != 1 {
			t.Errorf("expected 1 embedding, got %d", len(resp.Data))
		}
	})

	t.Run("Models", func(t *testing.T) {
		resp, err := mock.Models(ctx)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if len(resp.Data) != 2 {
			t.Errorf("expected 2 models, got %d", len(resp.Data))
		}
	})

	t.Run("Health", func(t *testing.T) {
		err := mock.Health(ctx)
		if err != nil {
			t.Errorf("expected healthy, got error: %v", err)
		}

		// Test with error
		mock.SetHealthError(connectors.ErrHealthCheckFailed)
		err = mock.Health(ctx)
		if !errors.Is(err, connectors.ErrHealthCheckFailed) {
			t.Errorf("expected ErrHealthCheckFailed, got %v", err)
		}
	})

	t.Run("Context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		req := &connectors.ChatRequest{
			Model: "mock-model",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}

		_, err := mock.Chat(ctx, req)
		if !errors.Is(err, connectors.ErrContextCanceled) {
			t.Errorf("expected ErrContextCanceled, got %v", err)
		}
	})
}
