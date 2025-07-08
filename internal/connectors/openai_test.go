package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewOpenAIConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config with custom base URL",
			config: ProviderConfig{
				BaseURL:        "https://custom.openai.com",
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
				BaseURL: "https://api.openai.com",
				APIKey:  "test-key",
			},
			wantErr: false, // Validate sets defaults
		},
		{
			name: "empty base URL uses default",
			config: ProviderConfig{
				BaseURL:        "", // Should use default
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewOpenAIConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewOpenAIConnector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				if connector == nil {
					t.Error("NewOpenAIConnector() returned nil connector")
				}
				if connector.config.BaseURL == "" {
					t.Error("NewOpenAIConnector() base URL not set")
				}
			}
		})
	}
}

func TestOpenAIConnector_Chat(t *testing.T) {
	tests := []struct {
		name         string
		request      *ChatRequest
		mockResponse interface{}
		mockStatus   int
		wantErr      bool
	}{
		{
			name: "successful chat completion",
			request: &ChatRequest{
				Model: "gpt-4",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			mockResponse: ChatResponse{
				ID:      "chatcmpl-123",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "gpt-4",
				Choices: []Choice{
					{
						Index:        0,
						Message:      Message{Role: "assistant", Content: "Hi there!"},
						FinishReason: "stop",
					},
				},
				Usage: Usage{
					PromptTokens:     5,
					CompletionTokens: 3,
					TotalTokens:      8,
				},
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name: "API error response",
			request: &ChatRequest{
				Model: "gpt-4",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			mockResponse: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Invalid API key",
					"type":    "invalid_request_error",
					"code":    "invalid_api_key",
				},
			},
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name: "rate limit error",
			request: &ChatRequest{
				Model: "gpt-4",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			mockResponse: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Rate limit exceeded",
					"type":    "rate_limit_error",
				},
			},
			mockStatus: http.StatusTooManyRequests,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify headers
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("Expected Authorization header, got %s", r.Header.Get("Authorization"))
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Expected Content-Type header, got %s", r.Header.Get("Content-Type"))
				}

				// Verify request body
				var reqBody ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				}
				if reqBody.Stream {
					t.Error("Expected stream to be false for non-streaming request")
				}

				w.WriteHeader(tt.mockStatus)
				json.NewEncoder(w).Encode(tt.mockResponse)
			}))
			defer server.Close()

			connector, _ := NewOpenAIConnector(ProviderConfig{
				BaseURL:        server.URL + "/v1",
				APIKey:         "test-key",
				Timeout:        5 * time.Second,
				MaxConnections: 10,
				MaxRetries:     0,
			})

			resp, err := connector.Chat(context.Background(), tt.request)
			if (err != nil) != tt.wantErr {
				t.Errorf("Chat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && resp != nil {
				if resp.ID == "" {
					t.Error("Chat() response missing ID")
				}
				if len(resp.Choices) == 0 {
					t.Error("Chat() response missing choices")
				}
			}
		})
	}
}

func TestOpenAIConnector_ChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify streaming request
		var reqBody ChatRequest
		json.NewDecoder(r.Body).Decode(&reqBody)
		if !reqBody.Stream {
			t.Error("Expected stream to be true for streaming request")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send SSE chunks
		chunks := []string{
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" world!"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}

		for _, chunk := range chunks {
			w.Write([]byte(chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	connector, _ := NewOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	stream, err := connector.ChatStream(context.Background(), &ChatRequest{
		Model: "gpt-4",
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

	if content != "Hello world!" {
		t.Errorf("Expected 'Hello world!', got '%s'", content)
	}
	if chunkCount != 4 { // 4 chunks before [DONE]
		t.Errorf("Expected 4 chunks, got %d", chunkCount)
	}
}

func TestOpenAIConnector_Embeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody EmbeddingsRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := EmbeddingsResponse{
			Object: "list",
			Data: []Embedding{
				{
					Object:    "embedding",
					Index:     0,
					Embedding: []float32{0.1, 0.2, 0.3},
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

	connector, _ := NewOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	resp, err := connector.Embeddings(context.Background(), &EmbeddingsRequest{
		Model: "text-embedding-ada-002",
		Input: "Hello world",
	})
	if err != nil {
		t.Fatalf("Embeddings() error = %v", err)
	}

	if len(resp.Data) != 1 {
		t.Errorf("Expected 1 embedding, got %d", len(resp.Data))
	}
	if len(resp.Data[0].Embedding) != 3 {
		t.Errorf("Expected 3 dimensions, got %d", len(resp.Data[0].Embedding))
	}
}

func TestOpenAIConnector_Models(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ModelsResponse{
			Object: "list",
			Data: []Model{
				{
					ID:      "gpt-4",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "openai",
				},
				{
					ID:      "gpt-3.5-turbo",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "openai",
				},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	resp, err := connector.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}

	if len(resp.Data) != 2 {
		t.Errorf("Expected 2 models, got %d", len(resp.Data))
	}
}

func TestOpenAIConnector_Health(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			resp := ModelsResponse{
				Object: "list",
				Data:   []Model{},
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	connector, _ := NewOpenAIConnector(ProviderConfig{
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

func TestOpenAIConnector_RetryLogic(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ModelsResponse{Object: "list", Data: []Model{}})
	}))
	defer server.Close()

	connector, _ := NewOpenAIConnector(ProviderConfig{
		BaseURL:         server.URL + "/v1",
		APIKey:          "test-key",
		Timeout:         5 * time.Second,
		MaxConnections:  10,
		MaxRetries:      3,
		RetryDelay:      10 * time.Millisecond,
		BackoffMultiplier: 2.0,
	})

	err := connector.Health(context.Background())
	if err != nil {
		t.Errorf("Health() with retry error = %v", err)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestOpenAIConnector_Name(t *testing.T) {
	connector, err := NewOpenAIConnector(ProviderConfig{
		APIKey: "test-key",
		Timeout: 5 * time.Second,
		MaxConnections: 10,
	})
	if err != nil {
		t.Fatalf("NewOpenAIConnector() error = %v", err)
	}
	
	if name := connector.Name(); name != "openai" {
		t.Errorf("Expected name 'openai', got '%s'", name)
	}
}

func TestOpenAIConnector_Close(t *testing.T) {
	connector, err := NewOpenAIConnector(ProviderConfig{
		APIKey: "test-key",
		Timeout: 5 * time.Second,
		MaxConnections: 10,
	})
	if err != nil {
		t.Fatalf("NewOpenAIConnector() error = %v", err)
	}
	
	if err := connector.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestOpenAIConnector_ChatStreamErrors(t *testing.T) {
	tests := []struct {
		name       string
		mockStatus int
		mockError  bool
		wantErr    bool
	}{
		{
			name:       "request preparation error",
			mockStatus: http.StatusBadRequest,
			mockError:  true,
			wantErr:    true,
		},
		{
			name:       "server error",
			mockStatus: http.StatusInternalServerError,
			mockError:  true,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.mockError {
					w.WriteHeader(tt.mockStatus)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error": map[string]interface{}{
							"message": "Test error",
							"type":    "error",
						},
					})
					return
				}
			}))
			defer server.Close()

			connector, _ := NewOpenAIConnector(ProviderConfig{
				BaseURL:        server.URL + "/v1",
				APIKey:         "test-key",
				Timeout:        5 * time.Second,
				MaxConnections: 10,
				MaxRetries:     0,
			})

			_, err := connector.ChatStream(context.Background(), &ChatRequest{
				Model: "gpt-3.5-turbo",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			})

			if (err != nil) != tt.wantErr {
				t.Errorf("ChatStream() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOpenAIConnector_EmbeddingsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"message": "Invalid model",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	connector, _ := NewOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     0,
	})

	_, err := connector.Embeddings(context.Background(), &EmbeddingsRequest{
		Model: "invalid-model",
		Input: "test",
	})

	if err == nil {
		t.Error("Expected error for invalid model")
	}
	if apiErr, ok := err.(*APIError); ok {
		if apiErr.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", apiErr.StatusCode)
		}
	} else {
		t.Errorf("Expected APIError, got %T", err)
	}
}

func TestOpenAIConnector_ModelsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"message": "Invalid API key",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	connector, _ := NewOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		APIKey:         "invalid-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     0,
	})

	_, err := connector.Models(context.Background())
	if err == nil {
		t.Error("Expected error for invalid API key")
	}
}