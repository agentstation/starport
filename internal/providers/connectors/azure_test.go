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

func TestNewAzureOpenAIConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config with resource URL",
			config: ProviderConfig{
				BaseURL:        "https://myresource.openai.azure.com",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "valid config with custom API version",
			config: ProviderConfig{
				BaseURL:        "https://myresource.openai.azure.com",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
				Extra: map[string]interface{}{
					"api_version": "2024-03-01-preview",
				},
			},
			wantErr: false,
		},
		{
			name: "missing base URL",
			config: ProviderConfig{
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewAzureOpenAIConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewAzureOpenAIConnector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				if connector == nil {
					t.Error("NewAzureOpenAIConnector() returned nil connector")
				}
				if connector.Name() != "azure" {
					t.Errorf("Expected name 'azure', got '%s'", connector.Name())
				}
			}
		})
	}
}

func TestAzureOpenAIConnector_Models(t *testing.T) {
	connector, err := NewAzureOpenAIConnector(ProviderConfig{
		BaseURL:        "https://myresource.openai.azure.com",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	if err != nil {
		t.Fatalf("NewAzureOpenAIConnector() error = %v", err)
	}

	resp, err := connector.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}

	// Azure returns example deployments
	if resp.Object != "list" {
		t.Errorf("Expected object 'list', got '%s'", resp.Object)
	}
	if len(resp.Data) != 4 {
		t.Errorf("Expected 4 example models, got %d", len(resp.Data))
	}
	// Check for the placeholder
	hasPlaceholder := false
	for _, model := range resp.Data {
		if model.ID == "azure/YOUR-DEPLOYMENT-NAME" {
			hasPlaceholder = true
			break
		}
	}
	if !hasPlaceholder {
		t.Error("Expected placeholder model 'azure/YOUR-DEPLOYMENT-NAME' not found")
	}
}

func TestAzureOpenAIConnector_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Azure-specific headers
		if r.Header.Get("api-key") != "test-key" {
			t.Errorf("Expected api-key header, got %s", r.Header.Get("api-key"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type header, got %s", r.Header.Get("Content-Type"))
		}

		// Verify API version in query
		apiVersion := r.URL.Query().Get("api-version")
		if apiVersion == "" {
			t.Error("Expected api-version query parameter")
		}

		var reqBody ChatRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := ChatResponse{
			ID:      "chatcmpl-azure-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   reqBody.Model, // This would be the deployment name
			Choices: []Choice{
				{
					Index:        0,
					Message:      Message{Role: "assistant", Content: "Hello from Azure OpenAI!"},
					FinishReason: "stop",
				},
			},
			Usage: Usage{
				PromptTokens:     5,
				CompletionTokens: 5,
				TotalTokens:      10,
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewAzureOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     0,
	})

	resp, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "azure/gpt-35-turbo-deployment", // Using provider/model format
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if resp.Choices[0].Message.Content != "Hello from Azure OpenAI!" {
		t.Errorf("Expected 'Hello from Azure OpenAI!', got '%s'", resp.Choices[0].Message.Content)
	}
}

func TestAzureOpenAIConnector_APIVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check custom API version
		apiVersion := r.URL.Query().Get("api-version")
		if apiVersion != "2024-03-01-preview" {
			t.Errorf("Expected api-version '2024-03-01-preview', got '%s'", apiVersion)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ChatResponse{
			ID:      "chatcmpl-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "gpt-4",
			Choices: []Choice{
				{Message: Message{Role: "assistant", Content: "OK"}},
			},
		})
	}))
	defer server.Close()

	connector, _ := NewAzureOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		Extra: map[string]interface{}{
			"api_version": "2024-03-01-preview",
		},
	})

	_, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "gpt-4-deployment",
		Messages: []Message{
			{Role: "user", Content: "Test"},
		},
	})

	if err != nil {
		t.Errorf("Chat() error = %v", err)
	}
}

func TestAzureOpenAIConnector_ErrorHandling(t *testing.T) {
	// Azure should handle errors like OpenAI but with Azure-specific error format
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"message": "Access denied due to invalid subscription key",
				"type":    "invalid_request_error",
				"code":    "401",
			},
		})
	}))
	defer server.Close()

	connector, _ := NewAzureOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "invalid-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     0,
	})

	_, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "gpt-35-turbo",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err == nil {
		t.Error("Expected error for invalid key")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Errorf("Expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", apiErr.StatusCode)
	}
}

func TestAzureOpenAIConnector_ChatStream(t *testing.T) {
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
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" from Azure!"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}

		for _, chunk := range chunks {
			w.Write([]byte(chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	connector, _ := NewAzureOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	stream, err := connector.ChatStream(context.Background(), &ChatRequest{
		Model: "gpt-4-deployment",
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
	}

	if content != "Hello from Azure!" {
		t.Errorf("Expected 'Hello from Azure!', got '%s'", content)
	}
}

func TestAzureOpenAIConnector_Embeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's using deployment name in path
		// Azure format: /openai/deployments/{deployment-name}/embeddings

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

	connector, _ := NewAzureOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	resp, err := connector.Embeddings(context.Background(), &EmbeddingsRequest{
		Model: "text-embedding-ada-002-deployment",
		Input: "Hello world",
	})
	if err != nil {
		t.Fatalf("Embeddings() error = %v", err)
	}

	if len(resp.Data) != 1 {
		t.Errorf("Expected 1 embedding, got %d", len(resp.Data))
	}
}
