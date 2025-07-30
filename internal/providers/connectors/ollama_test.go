package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOllamaConnector_Chat(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			// Verify request
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			// Check that stream is false
			if stream, ok := req["stream"].(bool); !ok || stream {
				http.Error(w, "expected stream=false", http.StatusBadRequest)
				return
			}

			// Send response
			resp := map[string]any{
				"model":      req["model"],
				"created_at": time.Now().Format(time.RFC3339),
				"message": map[string]string{
					"role":    "assistant",
					"content": "Hello! How can I help you?",
				},
				"total_duration":       1000000000, // 1 second in nanoseconds
				"load_duration":        100000000,
				"prompt_eval_count":    10,
				"prompt_eval_duration": 50000000,
				"eval_count":           15,
				"eval_duration":        100000000,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create connector
	config := ProviderConfig{
		BaseURL:        server.URL,
		Timeout:        10 * time.Second,
		MaxConnections: 10,
		Enabled:        true,
	}
	connector, err := NewOllamaConnector(config)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer connector.Close()

	// Test chat
	ctx := context.Background()
	req := &ChatRequest{
		Model: "ollama/llama2",
		Messages: []Message{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Temperature: &[]float32{0.7}[0],
		MaxTokens:   &[]int{100}[0],
	}

	resp, err := connector.Chat(ctx, req)
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	// Verify response
	if !strings.HasPrefix(resp.ID, "ollama-") {
		t.Errorf("expected ID to start with 'ollama-', got %s", resp.ID)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("expected object='chat.completion', got %s", resp.Object)
	}
	if resp.Model != "ollama/llama2" {
		t.Errorf("expected model='ollama/llama2', got %s", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello! How can I help you?" {
		t.Errorf("unexpected message content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens == 0 {
		t.Fatal("expected usage data")
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("expected prompt tokens=10, got %d", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 15 {
		t.Errorf("expected completion tokens=15, got %d", resp.Usage.CompletionTokens)
	}
}

func TestOllamaConnector_ChatStream(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			// Verify request
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			// Check that stream is true
			if stream, ok := req["stream"].(bool); !ok || !stream {
				http.Error(w, "expected stream=true", http.StatusBadRequest)
				return
			}

			// Send streaming response
			w.Header().Set("Content-Type", "application/x-ndjson")

			// Send chunks
			chunks := []map[string]any{
				{
					"model":      req["model"],
					"created_at": time.Now().Format(time.RFC3339),
					"message": map[string]string{
						"role":    "assistant",
						"content": "Hello",
					},
					"done": false,
				},
				{
					"model":      req["model"],
					"created_at": time.Now().Format(time.RFC3339),
					"message": map[string]string{
						"role":    "assistant",
						"content": "! How",
					},
					"done": false,
				},
				{
					"model":      req["model"],
					"created_at": time.Now().Format(time.RFC3339),
					"message": map[string]string{
						"role":    "assistant",
						"content": " can I help?",
					},
					"done": false,
				},
				{
					"model":      req["model"],
					"created_at": time.Now().Format(time.RFC3339),
					"done":       true,
				},
			}

			for _, chunk := range chunks {
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "%s\n", data)
			}

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create connector
	config := ProviderConfig{
		BaseURL:        server.URL,
		Timeout:        10 * time.Second,
		MaxConnections: 10,
		Enabled:        true,
	}
	connector, err := NewOllamaConnector(config)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer connector.Close()

	// Test streaming chat
	ctx := context.Background()
	req := &ChatRequest{
		Model: "ollama/llama2",
		Messages: []Message{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}

	stream, err := connector.ChatStream(ctx, req)
	if err != nil {
		t.Fatalf("chat stream failed: %v", err)
	}
	defer stream.Close()

	// Read chunks
	var contents []string
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read chunk: %v", err)
		}

		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			contents = append(contents, chunk.Choices[0].Delta.Content)
		}
	}

	// Verify we got all content
	fullContent := strings.Join(contents, "")
	if fullContent != "Hello! How can I help?" {
		t.Errorf("expected 'Hello! How can I help?', got '%s'", fullContent)
	}
}

func TestOllamaConnector_Models(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			// Send model list
			resp := map[string]any{
				"models": []map[string]any{
					{
						"name":        "llama2",
						"modified_at": time.Now().Format(time.RFC3339),
						"size":        4000000000,
						"digest":      "abc123",
						"details": map[string]any{
							"format":             "gguf",
							"family":             "llama",
							"families":           []string{"llama"},
							"parameter_size":     "7B",
							"quantization_level": "Q4_0",
						},
					},
					{
						"name":        "mistral",
						"modified_at": time.Now().Add(-time.Hour).Format(time.RFC3339),
						"size":        7000000000,
						"digest":      "def456",
						"details": map[string]any{
							"format":             "gguf",
							"family":             "mistral",
							"families":           []string{"mistral"},
							"parameter_size":     "7B",
							"quantization_level": "Q4_0",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create connector
	config := ProviderConfig{
		BaseURL:        server.URL,
		Timeout:        10 * time.Second,
		MaxConnections: 10,
		Enabled:        true,
	}
	connector, err := NewOllamaConnector(config)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer connector.Close()

	// Test models
	ctx := context.Background()
	resp, err := connector.Models(ctx)
	if err != nil {
		t.Fatalf("models failed: %v", err)
	}

	// Verify response
	if resp.Object != "list" {
		t.Errorf("expected object='list', got %s", resp.Object)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Data))
	}

	// Check first model
	if resp.Data[0].ID != "ollama/llama2" {
		t.Errorf("expected ID='ollama/llama2', got %s", resp.Data[0].ID)
	}
	if resp.Data[0].OwnedBy != "ollama" {
		t.Errorf("expected OwnedBy='ollama', got %s", resp.Data[0].OwnedBy)
	}

	// Check second model
	if resp.Data[1].ID != "ollama/mistral" {
		t.Errorf("expected ID='ollama/mistral', got %s", resp.Data[1].ID)
	}

	// Test caching - second call should be cached
	resp2, err := connector.Models(ctx)
	if err != nil {
		t.Fatalf("models (cached) failed: %v", err)
	}
	if len(resp2.Data) != 2 {
		t.Errorf("expected cached response to have 2 models")
	}
}

func TestOllamaConnector_Health(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			// Return empty model list for health check
			resp := map[string]any{
				"models": []any{},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create connector
	config := ProviderConfig{
		BaseURL:        server.URL,
		Timeout:        10 * time.Second,
		MaxConnections: 10,
		Enabled:        true,
	}
	connector, err := NewOllamaConnector(config)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer connector.Close()

	// Test health
	ctx := context.Background()
	err = connector.Health(ctx)
	if err != nil {
		t.Errorf("health check failed: %v", err)
	}
}

func TestOllamaConnector_ErrorHandling(t *testing.T) {
	// Create test server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			// Return error response
			resp := map[string]string{
				"error": "model not found",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create connector
	config := ProviderConfig{
		BaseURL:        server.URL,
		Timeout:        10 * time.Second,
		MaxConnections: 10,
		Enabled:        true,
	}
	connector, err := NewOllamaConnector(config)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer connector.Close()

	// Test chat with error
	ctx := context.Background()
	req := &ChatRequest{
		Model: "ollama/unknown-model",
		Messages: []Message{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
	}

	_, err = connector.Chat(ctx, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected status code 404, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "model not found" {
		t.Errorf("expected message 'model not found', got '%s'", apiErr.Message)
	}
	if apiErr.Provider != "ollama" {
		t.Errorf("expected provider 'ollama', got '%s'", apiErr.Provider)
	}
}

func TestOllamaConnector_DisabledError(t *testing.T) {
	// Try to create connector when not enabled
	config := ProviderConfig{
		BaseURL: "http://localhost:11434",
		Enabled: false,
	}

	_, err := NewConnector("ollama", config)
	if err == nil {
		t.Fatal("expected error when ollama not enabled")
	}

	if !strings.Contains(err.Error(), "ollama support is not enabled") {
		t.Errorf("expected 'ollama support is not enabled' error, got: %v", err)
	}
}
