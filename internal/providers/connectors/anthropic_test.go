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

func TestNewAnthropicConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config with custom base URL",
			config: ProviderConfig{
				BaseURL:        "https://custom.anthropic.com",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "missing catalog base URL",
			config: ProviderConfig{
				Timeout:        30 * time.Second,
				MaxConnections: 100,
				BaseURL:        "",
			},
			wantErr: true,
		},
		{
			name: "invalid config - missing timeout",
			config: ProviderConfig{
				BaseURL: "https://api.anthropic.com",
			},
			wantErr: false, // Validate sets defaults
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewAnthropicConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewAnthropicConnector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				if connector == nil {
					t.Error("NewAnthropicConnector() returned nil connector")
				}
				if connector.config.BaseURL == "" {
					t.Error("NewAnthropicConnector() base URL not set")
				}
			}
		})
	}
}

func TestAnthropicConnector_Chat(t *testing.T) {
	tests := []struct {
		name         string
		request      *ChatRequest
		mockResponse any
		mockStatus   int
		wantErr      bool
	}{
		{
			name: "successful chat completion",
			request: &ChatRequest{Credential: testAnthropicMaterial("test-key"),
				Model: "claude-3-haiku-20240307",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			mockResponse: anthropicResponse{
				ID:   "msg_123",
				Type: "message",
				Role: "assistant",
				Content: []anthropicContent{
					{Type: "text", Text: "Hi there! How can I help you today?"},
				},
				Model:      "claude-3-haiku-20240307",
				StopReason: "end_turn",
				Usage: anthropicUsage{
					InputTokens:  10,
					OutputTokens: 8,
				},
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name: "chat with system message",
			request: &ChatRequest{Credential: testAnthropicMaterial("test-key"),
				Model: "claude-3-haiku-20240307",
				Messages: []Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			},
			mockResponse: anthropicResponse{
				ID:   "msg_124",
				Type: "message",
				Role: "assistant",
				Content: []anthropicContent{
					{Type: "text", Text: "Hello! I'm here to help."},
				},
				Model:      "claude-3-haiku-20240307",
				StopReason: "end_turn",
				Usage: anthropicUsage{
					InputTokens:  15,
					OutputTokens: 6,
				},
			},
			mockStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name: "API error response",
			request: &ChatRequest{Credential: testAnthropicMaterial("test-key"),
				Model: "claude-3-haiku-20240307",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			mockResponse: map[string]any{
				"error": map[string]any{
					"type":    "invalid_request_error",
					"message": "Invalid API key",
				},
			},
			mockStatus: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name: "rate limit error",
			request: &ChatRequest{Credential: testAnthropicMaterial("test-key"),
				Model: "claude-3-haiku-20240307",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			mockResponse: map[string]any{
				"error": map[string]any{
					"type":    "rate_limit_error",
					"message": "Rate limit exceeded",
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
				if r.Header.Get("x-api-key") != "test-key" {
					t.Errorf("Expected x-api-key header, got %s", r.Header.Get("x-api-key"))
				}
				if r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Errorf("Expected anthropic-version header, got %s", r.Header.Get("anthropic-version"))
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Expected Content-Type header, got %s", r.Header.Get("Content-Type"))
				}

				// Verify request body
				var reqBody map[string]any
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				}
				if reqBody["stream"].(bool) {
					t.Error("Expected stream to be false for non-streaming request")
				}

				// Check for system message conversion
				if system, ok := reqBody["system"]; ok && tt.name == "chat with system message" {
					if system != "You are a helpful assistant." {
						t.Errorf("Expected system message, got %v", system)
					}
				}

				w.WriteHeader(tt.mockStatus)
				json.NewEncoder(w).Encode(tt.mockResponse)
			}))
			defer server.Close()

			connector, _ := NewAnthropicConnector(ProviderConfig{
				BaseURL:        server.URL + "/v1",
				Timeout:        5 * time.Second,
				MaxConnections: 10,
			})
			tt.request.Endpoint = InferenceEndpoint{Type: "anthropic", URL: server.URL + "/v1/messages"}

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
				if resp.Object != "chat.completion" {
					t.Errorf("Expected object 'chat.completion', got '%s'", resp.Object)
				}
			}
		})
	}
}

func TestAnthropicConnector_ChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify streaming request
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		if !reqBody["stream"].(bool) {
			t.Error("Expected stream to be true for streaming request")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send SSE chunks in Anthropic format
		chunks := []string{
			`data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-3-haiku-20240307"}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world!"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`data: {"type":"message_stop"}`,
		}

		for _, chunk := range chunks {
			w.Write([]byte(chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	connector, _ := NewAnthropicConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	stream, err := connector.ChatStream(context.Background(), &ChatRequest{Credential: testAnthropicMaterial("test-key"),
		Model:    "claude-3-haiku-20240307",
		Endpoint: InferenceEndpoint{Type: "anthropic", URL: server.URL + "/v1/messages"},
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
	var hasRole bool
	chunkCount := 0
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Stream closes on message_stop
			if chunkCount > 0 {
				break
			}
			t.Fatalf("Recv() error = %v", err)
		}
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta.Role == "assistant" {
				hasRole = true
			}
			if chunk.Choices[0].Delta.Content != "" {
				content += chunk.Choices[0].Delta.Content
			}
		}
		chunkCount++
	}

	if !hasRole {
		t.Error("Expected role in first chunk")
	}
	if content != "Hello world!" {
		t.Errorf("Expected 'Hello world!', got '%s'", content)
	}
}

func TestAnthropicStreamReportsPromptTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":10,"cache_read_input_tokens":5,"cache_creation_input_tokens":2}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			`data: {"type":"message_stop"}`,
		}
		for _, chunk := range chunks {
			w.Write([]byte(chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	connector, _ := NewAnthropicConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	stream, err := connector.ChatStream(context.Background(), &ChatRequest{Credential: testAnthropicMaterial("test-key"),
		Model:    "claude-3-haiku-20240307",
		Endpoint: InferenceEndpoint{Type: "anthropic", URL: server.URL + "/v1/messages"},
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	defer stream.Close()

	var usage *Usage
	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}

	if usage == nil {
		t.Fatal("Expected a streamed usage payload")
	}
	// Anthropic reports prompt-side usage only in message_start; the stream
	// must latch it and compose it with message_delta output tokens.
	// PromptTokens must include cache reads and cache writes.
	if usage.PromptTokens != 17 {
		t.Errorf("PromptTokens = %d, want 17", usage.PromptTokens)
	}
	if usage.CompletionTokens != 7 {
		t.Errorf("CompletionTokens = %d, want 7", usage.CompletionTokens)
	}
	if usage.TotalTokens != 24 {
		t.Errorf("TotalTokens = %d, want 24", usage.TotalTokens)
	}
	if usage.CacheWriteTokens != 2 {
		t.Errorf("CacheWriteTokens = %d, want 2", usage.CacheWriteTokens)
	}
	if usage.PromptTokensDetails == nil || usage.PromptTokensDetails.CachedTokens != 5 {
		t.Errorf("PromptTokensDetails = %+v, want CachedTokens 5", usage.PromptTokensDetails)
	}
}

func TestAnthropicConnector_Embeddings(t *testing.T) {
	connector, _ := NewAnthropicConnector(ProviderConfig{
		BaseURL:        "https://provider.test",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	_, err := connector.Embeddings(context.Background(), &EmbeddingsRequest{Credential: testAnthropicMaterial("test-key"),
		Model: "text-embedding-ada-002",
		Input: "Hello world",
	})

	if err == nil {
		t.Error("Expected error for unsupported embeddings endpoint")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Errorf("Expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotImplemented {
		t.Errorf("Expected status 501, got %d", apiErr.StatusCode)
	}
}

func TestAnthropicConnector_ConvertMessageContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)

		// Check multimodal content conversion
		messages := reqBody["messages"].([]any)
		msg := messages[0].(map[string]any)
		content := msg["content"].([]any)

		if len(content) != 2 {
			t.Errorf("Expected 2 content parts, got %d", len(content))
		}

		// Check text part
		textPart := content[0].(map[string]any)
		if textPart["type"] != "text" {
			t.Errorf("Expected text type, got %v", textPart["type"])
		}

		// Check image part
		imagePart := content[1].(map[string]any)
		if imagePart["type"] != "image" {
			t.Errorf("Expected image type, got %v", imagePart["type"])
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(anthropicResponse{
			ID:   "msg_125",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContent{
				{Type: "text", Text: "I see the image."},
			},
		})
	}))
	defer server.Close()

	connector, _ := NewAnthropicConnector(ProviderConfig{
		BaseURL: server.URL + "/v1",
	})

	// Test multimodal content
	_, err := connector.Chat(context.Background(), &ChatRequest{Credential: testAnthropicMaterial("test-key"),
		Model:    "claude-3-haiku-20240307",
		Endpoint: InferenceEndpoint{Type: "anthropic", URL: server.URL + "/v1/messages"},
		Messages: []Message{
			{
				Role: "user",
				Content: []ContentPart{
					{Type: "text", Text: "What's in this image?"},
					{Type: "image_url", ImageURL: &ImageURL{URL: "base64data"}},
				},
			},
		},
	})

	if err != nil {
		t.Errorf("Chat() with multimodal content error = %v", err)
	}
}

func TestAnthropicConnector_Name(t *testing.T) {
	connector, err := NewAnthropicConnector(ProviderConfig{
		BaseURL:        "https://provider.test",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	if err != nil {
		t.Fatalf("NewAnthropicConnector() error = %v", err)
	}

	if name := connector.Name(); name != "anthropic" {
		t.Errorf("Expected name 'anthropic', got '%s'", name)
	}
}

func TestAnthropicConnector_Close(t *testing.T) {
	connector, err := NewAnthropicConnector(ProviderConfig{
		BaseURL:        "https://provider.test",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	if err != nil {
		t.Fatalf("NewAnthropicConnector() error = %v", err)
	}

	if err := connector.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
