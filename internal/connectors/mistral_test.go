package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewMistralConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config with custom base URL",
			config: ProviderConfig{
				BaseURL:        "https://custom.mistral.ai",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "valid config with default base URL",
			config: ProviderConfig{
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "invalid config - missing timeout",
			config: ProviderConfig{
				APIKey: "test-key",
			},
			wantErr: false, // Validate sets defaults
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewMistralConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewMistralConnector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				if connector == nil {
					t.Error("NewMistralConnector() returned nil connector")
				}
				if connector.config.BaseURL == "" {
					t.Error("NewMistralConnector() base URL not set")
				}
			}
		})
	}
}

func TestMistralConnector_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Authorization header, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type header, got %s", r.Header.Get("Content-Type"))
		}

		var reqBody ChatRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := ChatResponse{
			ID:      "chatcmpl-mistral-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   reqBody.Model,
			Choices: []Choice{
				{
					Index:        0,
					Message:      Message{Role: "assistant", Content: "Bonjour from Mistral!"},
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

	connector, _ := NewMistralConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     0,
	})

	resp, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "mistral-tiny",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if resp.Choices[0].Message.Content != "Bonjour from Mistral!" {
		t.Errorf("Expected 'Bonjour from Mistral!', got '%s'", resp.Choices[0].Message.Content)
	}
}

func TestMistralConnector_ChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody ChatRequest
		json.NewDecoder(r.Body).Decode(&reqBody)
		if !reqBody.Stream {
			t.Error("Expected stream to be true for streaming request")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send SSE chunks
		chunks := []string{
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"mistral-tiny","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"mistral-tiny","choices":[{"index":0,"delta":{"content":"Bonjour"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"mistral-tiny","choices":[{"index":0,"delta":{"content":" world!"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"mistral-tiny","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}

		for _, chunk := range chunks {
			w.Write([]byte(chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	connector, _ := NewMistralConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	stream, err := connector.ChatStream(context.Background(), &ChatRequest{
		Model: "mistral-tiny",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	defer stream.Close()

	// Read chunks
	var content string
	chunkCount := 0
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			content += chunk.Choices[0].Delta.Content
		}
		chunkCount++
	}

	if content != "Bonjour world!" {
		t.Errorf("Expected 'Bonjour world!', got '%s'", content)
	}
	if chunkCount != 4 { // 4 chunks before [DONE]
		t.Errorf("Expected 4 chunks, got %d", chunkCount)
	}
}

func TestMistralConnector_Embeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody EmbeddingsRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := EmbeddingsResponse{
			Object: "list",
			Data: []Embedding{
				{
					Object:    "embedding",
					Index:     0,
					Embedding: []float32{0.1, 0.2, 0.3, 0.4},
				},
			},
			Model: reqBody.Model,
			Usage: Usage{
				PromptTokens: 5,
				TotalTokens:  5,
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewMistralConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	resp, err := connector.Embeddings(context.Background(), &EmbeddingsRequest{
		Model: "mistral-embed",
		Input: "Hello world",
	})
	if err != nil {
		t.Fatalf("Embeddings() error = %v", err)
	}

	if len(resp.Data) != 1 {
		t.Errorf("Expected 1 embedding, got %d", len(resp.Data))
	}
	if len(resp.Data[0].Embedding) != 4 {
		t.Errorf("Expected 4 dimensions, got %d", len(resp.Data[0].Embedding))
	}
}

func TestMistralConnector_Models(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ModelsResponse{
			Object: "list",
			Data: []Model{
				{
					ID:      "mistral-tiny",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "mistral",
				},
				{
					ID:      "mistral-small",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "mistral",
				},
				{
					ID:      "mistral-medium",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "mistral",
				},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewMistralConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	resp, err := connector.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}

	if len(resp.Data) != 3 {
		t.Errorf("Expected 3 models, got %d", len(resp.Data))
	}
}

func TestMistralConnector_Health(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			resp := ChatResponse{
				ID:      "test-123",
				Object:  "chat.completion",
				Created: 1234567890,
				Model:   "mistral/mistral-tiny",
				Choices: []Choice{
					{
						Index:        0,
						Message:      Message{Role: RoleAssistant, Content: "H"},
						FinishReason: "stop",
					},
				},
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	connector, _ := NewMistralConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	err := connector.Health(context.Background())
	if err != nil {
		t.Errorf("Health() error = %v", err)
	}
}

func TestMistralConnector_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Rate limit exceeded",
			"type":    "rate_limit_error",
			"code":    "rate_limit",
		})
	}))
	defer server.Close()

	connector, _ := NewMistralConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     0,
	})

	_, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "mistral-tiny",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err == nil {
		t.Error("Expected error for rate limit")
		return
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Errorf("Expected APIError, got %T", err)
		return
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", apiErr.StatusCode)
	}
	if apiErr.Provider != "mistral" {
		t.Errorf("Expected provider 'mistral', got '%s'", apiErr.Provider)
	}
}

func TestMistralConnector_Name(t *testing.T) {
	connector, _ := NewMistralConnector(ProviderConfig{
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	
	if name := connector.Name(); name != "mistral" {
		t.Errorf("Expected name 'mistral', got '%s'", name)
	}
}

func TestMistralConnector_Close(t *testing.T) {
	connector, _ := NewMistralConnector(ProviderConfig{
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	
	if err := connector.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}