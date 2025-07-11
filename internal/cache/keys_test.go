package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyGenerator(t *testing.T) {
	kg := NewKeyGenerator("test")

	t.Run("chat completion key generation", func(t *testing.T) {
		// Test basic request
		req1 := ChatCompletionRequest{
			Model: "gpt-4",
			Messages: []Message{
				{Role: "system", Content: "You are a helpful assistant"},
				{Role: "user", Content: "Hello"},
			},
		}

		key1 := kg.ChatCompletionKey(req1)
		assert.NotEmpty(t, key1)
		assert.Contains(t, key1, "test:chat:")

		// Same request should generate same key
		key2 := kg.ChatCompletionKey(req1)
		assert.Equal(t, key1, key2)

		// Different message should generate different key
		req2 := req1
		req2.Messages = append(req2.Messages, Message{Role: "assistant", Content: "Hi there!"})
		key3 := kg.ChatCompletionKey(req2)
		assert.NotEqual(t, key1, key3)

		// Different model should generate different key
		req3 := req1
		req3.Model = "gpt-3.5-turbo"
		key4 := kg.ChatCompletionKey(req3)
		assert.NotEqual(t, key1, key4)

		// Test with optional parameters
		temp := float32(0.7)
		maxTokens := 100
		req4 := req1
		req4.Temperature = &temp
		req4.MaxTokens = &maxTokens
		key5 := kg.ChatCompletionKey(req4)
		assert.NotEqual(t, key1, key5)
	})

	t.Run("embedding key generation", func(t *testing.T) {
		// Test with string input
		req1 := EmbeddingRequest{
			Model: "text-embedding-ada-002",
			Input: "Hello world",
		}

		key1 := kg.EmbeddingKey(req1)
		assert.NotEmpty(t, key1)
		assert.Contains(t, key1, "test:embedding:")

		// Same request should generate same key
		key2 := kg.EmbeddingKey(req1)
		assert.Equal(t, key1, key2)

		// Test with array input
		req2 := EmbeddingRequest{
			Model: "text-embedding-ada-002",
			Input: []string{"Hello", "world"},
		}
		key3 := kg.EmbeddingKey(req2)
		assert.NotEqual(t, key1, key3)

		// Array order should not matter (sorted internally)
		req3 := EmbeddingRequest{
			Model: "text-embedding-ada-002",
			Input: []string{"world", "Hello"},
		}
		key4 := kg.EmbeddingKey(req3)
		assert.Equal(t, key3, key4)

		// Test with dimensions
		dims := 256
		req4 := req1
		req4.Dimensions = &dims
		key5 := kg.EmbeddingKey(req4)
		assert.NotEqual(t, key1, key5)
	})

	t.Run("model list key generation", func(t *testing.T) {
		// All models
		key1 := kg.ModelListKey("")
		assert.Contains(t, key1, "test:models:")
		// Key contains a hash, not literal "all"

		// Provider-specific
		key2 := kg.ModelListKey("openai")
		assert.Contains(t, key2, "test:models:")
		assert.NotEqual(t, key1, key2)

		// Enhanced models
		key3 := kg.ModelListKey("enhanced")
		assert.Contains(t, key3, "test:models:")
		assert.NotEqual(t, key1, key3)
		assert.NotEqual(t, key2, key3)
	})

	t.Run("provider list key generation", func(t *testing.T) {
		key := kg.ProviderListKey()
		assert.Contains(t, key, "test:providers:")
		// Key contains a hash, not literal "all"

		// Should always generate same key
		key2 := kg.ProviderListKey()
		assert.Equal(t, key, key2)
	})

	t.Run("invalidation pattern generation", func(t *testing.T) {
		// Pattern for all chat completions
		pattern1 := kg.InvalidatePattern("chat", "")
		assert.Equal(t, "test:chat*", pattern1)

		// Pattern for specific model
		pattern2 := kg.InvalidatePattern("chat", "gpt-4")
		assert.Equal(t, "test:chat:gpt-4*", pattern2)

		// Pattern for embeddings
		pattern3 := kg.InvalidatePattern("embedding", "text-embedding-ada-002")
		assert.Equal(t, "test:embedding:text-embedding-ada-002*", pattern3)
	})

	t.Run("different namespaces generate different keys", func(t *testing.T) {
		kg2 := NewKeyGenerator("prod")

		req := ChatCompletionRequest{
			Model: "gpt-4",
			Messages: []Message{
				{Role: "user", Content: "Hello"},
			},
		}

		key1 := kg.ChatCompletionKey(req)
		key2 := kg2.ChatCompletionKey(req)

		assert.NotEqual(t, key1, key2)
		assert.Contains(t, key1, "test:")
		assert.Contains(t, key2, "prod:")
	})
}
