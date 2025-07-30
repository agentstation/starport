package connectors_test

import (
	"encoding/json"
	"testing"

	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestUnmarshalMessageContent(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantStr bool
		wantErr bool
	}{
		{
			name:    "string content",
			input:   `"Hello, world!"`,
			wantStr: true,
			wantErr: false,
		},
		{
			name:    "array content",
			input:   `[{"type":"text","text":"Hello"},{"type":"image_url","image_url":{"url":"http://example.com/image.jpg"}}]`,
			wantStr: false,
			wantErr: false,
		},
		{
			name:    "invalid content",
			input:   `{"invalid": "content"}`,
			wantStr: false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := connectors.UnmarshalMessageContent([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalMessageContent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantStr {
				if _, ok := content.(string); !ok {
					t.Errorf("expected string content, got %T", content)
				}
			} else if !tt.wantErr {
				if _, ok := content.([]connectors.ContentPart); !ok {
					t.Errorf("expected []ContentPart, got %T", content)
				}
			}
		})
	}
}

func TestChatRequestSerialization(t *testing.T) {
	temp := float32(0.7)
	maxTokens := 100

	req := connectors.ChatRequest{
		Model: "test-model",
		Messages: []connectors.Message{
			{
				Role:    "system",
				Content: "You are a helpful assistant.",
			},
			{
				Role:    "user",
				Content: "Hello!",
			},
		},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
		Stream:      true,
		Stop:        []string{"\n"},
		User:        "test-user",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal ChatRequest: %v", err)
	}

	var decoded connectors.ChatRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ChatRequest: %v", err)
	}

	if decoded.Model != req.Model {
		t.Errorf("expected model %s, got %s", req.Model, decoded.Model)
	}
	if len(decoded.Messages) != len(req.Messages) {
		t.Errorf("expected %d messages, got %d", len(req.Messages), len(decoded.Messages))
	}
	if decoded.Stream != req.Stream {
		t.Errorf("expected stream %v, got %v", req.Stream, decoded.Stream)
	}
}

func TestChatResponseSerialization(t *testing.T) {
	resp := connectors.ChatResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "test-model",
		Choices: []connectors.Choice{
			{
				Index: 0,
				Message: connectors.Message{
					Role:    "assistant",
					Content: "Hello! How can I help you?",
				},
				FinishReason: "stop",
			},
		},
		Usage: connectors.Usage{
			PromptTokens:     10,
			CompletionTokens: 7,
			TotalTokens:      17,
		},
		SystemFingerprint: "fp_123456",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal ChatResponse: %v", err)
	}

	var decoded connectors.ChatResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ChatResponse: %v", err)
	}

	if decoded.ID != resp.ID {
		t.Errorf("expected ID %s, got %s", resp.ID, decoded.ID)
	}
	if decoded.Model != resp.Model {
		t.Errorf("expected model %s, got %s", resp.Model, decoded.Model)
	}
	if decoded.Usage.TotalTokens != resp.Usage.TotalTokens {
		t.Errorf("expected total tokens %d, got %d", resp.Usage.TotalTokens, decoded.Usage.TotalTokens)
	}
}

func TestToolFunctionality(t *testing.T) {
	tool := connectors.Tool{
		Type: "function",
		Function: connectors.Function{
			Name:        "get_weather",
			Description: "Get the current weather",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "The city and state",
					},
				},
				"required": []string{"location"},
			},
		},
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("failed to marshal Tool: %v", err)
	}

	var decoded connectors.Tool
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal Tool: %v", err)
	}

	if decoded.Type != tool.Type {
		t.Errorf("expected type %s, got %s", tool.Type, decoded.Type)
	}
	if decoded.Function.Name != tool.Function.Name {
		t.Errorf("expected function name %s, got %s", tool.Function.Name, decoded.Function.Name)
	}
}

func TestStreamChunkSerialization(t *testing.T) {
	chunk := connectors.ChatStreamChunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "test-model",
		Choices: []connectors.StreamChoice{
			{
				Index: 0,
				Delta: connectors.MessageDelta{
					Content: "Hello",
				},
			},
		},
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("failed to marshal ChatStreamChunk: %v", err)
	}

	var decoded connectors.ChatStreamChunk
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ChatStreamChunk: %v", err)
	}

	if decoded.ID != chunk.ID {
		t.Errorf("expected ID %s, got %s", chunk.ID, decoded.ID)
	}
	if len(decoded.Choices) != len(chunk.Choices) {
		t.Errorf("expected %d choices, got %d", len(chunk.Choices), len(decoded.Choices))
	}
	if decoded.Choices[0].Delta.Content != chunk.Choices[0].Delta.Content {
		t.Errorf("expected delta content %s, got %s", chunk.Choices[0].Delta.Content, decoded.Choices[0].Delta.Content)
	}
}

func TestEmbeddingsSerialization(t *testing.T) {
	req := connectors.EmbeddingsRequest{
		Model: "text-embedding-ada-002",
		Input: "The quick brown fox",
		User:  "test-user",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal EmbeddingsRequest: %v", err)
	}

	var decoded connectors.EmbeddingsRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal EmbeddingsRequest: %v", err)
	}

	if decoded.Model != req.Model {
		t.Errorf("expected model %s, got %s", req.Model, decoded.Model)
	}

	// Test array input
	reqArray := connectors.EmbeddingsRequest{
		Model: "text-embedding-ada-002",
		Input: []string{"Hello", "World"},
	}

	data, err = json.Marshal(reqArray)
	if err != nil {
		t.Fatalf("failed to marshal EmbeddingsRequest with array: %v", err)
	}

	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal EmbeddingsRequest with array: %v", err)
	}
}
