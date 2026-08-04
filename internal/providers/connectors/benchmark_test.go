package connectors

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Helper functions for creating pointers
func Float32Ptr(f float32) *float32 { return &f }
func IntPtr(i int) *int             { return &i }

// BenchmarkConnectorOperations benchmarks various connector operations
func BenchmarkConnectorOperations(b *testing.B) {
	config := ProviderConfig{
		BaseURL:        "http://mock",
		APIKey:         "test-key",
		Timeout:        30 * time.Second,
		MaxConnections: 100,
	}

	connector := NewMockConnector(config)
	ctx := context.Background()

	// Prepare chat request
	chatReq := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello, how are you?"},
		},
		Temperature: Float32Ptr(0.7),
		MaxTokens:   IntPtr(100),
	}

	b.Run("Chat", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp, err := connector.Chat(ctx, chatReq)
			if err != nil {
				b.Fatal(err)
			}
			if resp == nil {
				b.Fatal("expected non-nil response")
			}
		}
	})

	b.Run("ChatStream", func(b *testing.B) {
		chatReq.Stream = true
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			stream, err := connector.ChatStream(ctx, chatReq)
			if err != nil {
				b.Fatal(err)
			}

			// Consume the stream
			for {
				_, err := stream.Recv()
				if err != nil {
					break
				}
			}
			stream.Close()
		}
	})

	// Prepare embeddings request
	embReq := &EmbeddingsRequest{
		Model: "text-embedding-model",
		Input: []string{
			"This is a test sentence for embedding.",
			"Another sentence to generate embeddings.",
		},
	}

	b.Run("Embeddings", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp, err := connector.Embeddings(ctx, embReq)
			if err != nil {
				b.Fatal(err)
			}
			if resp == nil || len(resp.Data) != 2 {
				b.Fatal("expected 2 embeddings")
			}
		}
	})

}

// BenchmarkModelParsing benchmarks model ID parsing
func BenchmarkModelParsing(b *testing.B) {
	modelIDs := []string{
		"openai/gpt-4",
		"anthropic/claude-3-opus",
		"google-ai-studio/gemini-pro",
		"groq/mixtral-8x7b",
		"mistral/mistral-large",
		"simple-model",
	}

	b.Run("ParseProviderModel", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			modelID := modelIDs[i%len(modelIDs)]
			// Simple provider/model parsing
			parts := strings.SplitN(modelID, "/", 2)
			var provider, model string
			if len(parts) == 2 {
				provider = parts[0]
				model = parts[1]
			} else {
				model = modelID
			}
			if provider == "" && model == "" {
				b.Fatal("failed to parse model")
			}
		}
	})
}

// BenchmarkStreamProcessing benchmarks stream chunk processing
func BenchmarkStreamProcessing(b *testing.B) {
	// Create a mock stream with pre-generated chunks
	chunks := make([]*ChatStreamChunk, 100)
	for i := range chunks {
		chunks[i] = &ChatStreamChunk{
			ID:      "test-stream",
			Object:  "chat.completion.chunk",
			Created: 1234567890,
			Model:   "test-model",
			Choices: []StreamChoice{
				{
					Index: 0,
					Delta: MessageDelta{
						Role:    "assistant",
						Content: "Word ",
					},
					FinishReason: "",
				},
			},
		}
	}

	b.Run("StreamChunkProcessing", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var content string
			for _, chunk := range chunks {
				if len(chunk.Choices) > 0 {
					content += chunk.Choices[0].Delta.Content
				}
			}
			if len(content) == 0 {
				b.Fatal("expected content")
			}
		}
	})
}

// BenchmarkErrorHandling benchmarks error creation and handling
func BenchmarkErrorHandling(b *testing.B) {
	b.Run("APIErrorCreation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			err := &APIError{
				StatusCode: 429,
				Type:       "rate_limit_exceeded",
				Message:    "Rate limit exceeded. Please try again later.",
				Provider:   "openai",
			}
			if !err.IsRetryable() {
				b.Fatal("expected retryable error")
			}
		}
	})

	b.Run("StreamErrorCreation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			err := &StreamError{
				Err:    ErrStreamClosed,
				Reason: "stream closed unexpectedly",
			}
			if err.Error() == "" {
				b.Fatal("expected error message")
			}
		}
	})
}
