package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewGroqConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config with custom base URL",
			config: ProviderConfig{
				BaseURL:        "https://custom.groq.com",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "missing catalog base URL",
			config: ProviderConfig{
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: true,
		},
		{
			name: "inherits OpenAI config validation",
			config: ProviderConfig{
				BaseURL: "https://provider.test",
				APIKey:  "test-key",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewGroqConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewGroqConnector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				if connector == nil {
					t.Error("NewGroqConnector() returned nil connector")
				}
				if connector.Name() != "groq" {
					t.Errorf("Expected name 'groq', got '%s'", connector.Name())
				}
			}
		})
	}
}

func TestGroqConnector_Embeddings(t *testing.T) {
	connector, _ := NewGroqConnector(ProviderConfig{
		BaseURL:        "https://provider.test",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	_, err := connector.Embeddings(context.Background(), &EmbeddingsRequest{
		Model: "some-model",
		Input: "test",
	})

	if err == nil {
		t.Error("Expected error for embeddings on Groq")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Errorf("Expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotImplemented {
		t.Errorf("Expected status 501, got %d", apiErr.StatusCode)
	}
	if apiErr.Provider != "groq" {
		t.Errorf("Expected provider 'groq', got '%s'", apiErr.Provider)
	}
}

func TestGroqConnector_Chat(t *testing.T) {
	// Since Groq uses OpenAI connector internally, we just need to verify
	// it properly delegates and maintains the groq name
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's using OpenAI-compatible endpoint
		if r.URL.Path != "/openai/v1/chat/completions" {
			t.Errorf("Expected path /openai/v1/chat/completions, got %s", r.URL.Path)
		}

		var reqBody ChatRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := ChatResponse{
			ID:      "chatcmpl-groq-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   reqBody.Model,
			Choices: []Choice{
				{
					Index:        0,
					Message:      Message{Role: "assistant", Content: "Hello from Groq!"},
					FinishReason: "stop",
				},
			},
			Usage: Usage{
				PromptTokens:     5,
				CompletionTokens: 4,
				TotalTokens:      9,
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewGroqConnector(ProviderConfig{
		BaseURL:        server.URL + "/openai/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	resp, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "llama-3.1-8b-instant",
		Endpoint: InferenceEndpoint{
			Type: "openai",
			URL:  server.URL + "/openai/v1/chat/completions",
		},
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if resp.Choices[0].Message.Content != "Hello from Groq!" {
		t.Errorf("Expected 'Hello from Groq!', got '%s'", resp.Choices[0].Message.Content)
	}
}
