package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agentstation/starport/internal/providers/connectors"
)

// KeyGenerator generates cache keys for different types of requests
type KeyGenerator struct {
	namespace string
}

// NewKeyGenerator creates a new key generator with the given namespace
func NewKeyGenerator(namespace string) *KeyGenerator {
	return &KeyGenerator{
		namespace: namespace,
	}
}

// ChatCompletionKey generates a cache key for chat completion requests.
// The key is based on the model, messages, and relevant parameters.
func (kg *KeyGenerator) ChatCompletionKey(req ChatCompletionRequest) string {
	// Create a normalized representation of the request
	normalized := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
	}

	// Add optional parameters that affect the response
	if req.Temperature != nil {
		normalized["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		normalized["max_tokens"] = *req.MaxTokens
	}
	if req.TopP != nil {
		normalized["top_p"] = *req.TopP
	}
	if req.N != nil && *req.N != 1 {
		normalized["n"] = *req.N
	}
	if req.Stop != nil {
		normalized["stop"] = req.Stop
	}
	if req.PresencePenalty != nil {
		normalized["presence_penalty"] = *req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		normalized["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.LogitBias != nil {
		normalized["logit_bias"] = req.LogitBias
	}
	if req.User != nil {
		normalized["user"] = *req.User
	}
	if req.Seed != nil {
		normalized["seed"] = *req.Seed
	}
	if req.Tools != nil {
		normalized["tools"] = req.Tools
	}
	if req.ToolChoice != nil {
		normalized["tool_choice"] = req.ToolChoice
	}
	if req.ResponseFormat != nil {
		normalized["response_format"] = req.ResponseFormat
	}

	return kg.generateKey("chat", normalized)
}

// EmbeddingKey generates a cache key for embedding requests.
func (kg *KeyGenerator) EmbeddingKey(req EmbeddingRequest) string {
	normalized := map[string]any{
		"model": req.Model,
		"input": kg.normalizeInput(req.Input),
	}

	if req.EncodingFormat != nil {
		normalized["encoding_format"] = *req.EncodingFormat
	}
	if req.Dimensions != nil {
		normalized["dimensions"] = *req.Dimensions
	}
	if req.User != nil {
		normalized["user"] = *req.User
	}

	return kg.generateKey("embedding", normalized)
}

// ModelListKey generates a cache key for model list requests.
func (kg *KeyGenerator) ModelListKey(provider string) string {
	if provider == "" {
		return kg.generateKey("models", map[string]any{"all": true})
	}
	return kg.generateKey("models", map[string]any{"provider": provider})
}

// ProviderListKey generates a cache key for provider list requests.
func (kg *KeyGenerator) ProviderListKey() string {
	return kg.generateKey("providers", map[string]any{"all": true})
}

// InvalidatePattern generates a pattern for cache invalidation.
// For example, InvalidatePattern("chat", "gpt-4") returns a pattern
// that matches all chat completion cache entries for gpt-4.
func (kg *KeyGenerator) InvalidatePattern(cacheType, model string) string {
	parts := []string{kg.namespace, cacheType}
	if model != "" {
		parts = append(parts, model)
	}
	return strings.Join(parts, ":") + "*"
}

// generateKey creates a deterministic cache key from the input data
func (kg *KeyGenerator) generateKey(cacheType string, data any) string {
	// Serialize the data to JSON for consistent hashing
	jsonData, err := json.Marshal(data)
	if err != nil {
		// Fallback to a simple string representation
		return fmt.Sprintf("%s:%s:%v", kg.namespace, cacheType, data)
	}

	// Generate SHA256 hash of the JSON data
	hash := sha256.Sum256(jsonData)
	hashStr := hex.EncodeToString(hash[:])

	// Create the cache key: namespace:type:hash
	return fmt.Sprintf("%s:%s:%s", kg.namespace, cacheType, hashStr[:16])
}

// normalizeInput normalizes the input field which can be string or []string
func (kg *KeyGenerator) normalizeInput(input any) any {
	switch v := input.(type) {
	case string:
		return []string{v}
	case []string:
		// Sort for consistent key generation
		sorted := make([]string, len(v))
		copy(sorted, v)
		sort.Strings(sorted)
		return sorted
	default:
		return input
	}
}

// Request types for key generation (minimal definitions)

// ChatCompletionRequest represents a chat completion request
type ChatCompletionRequest struct {
	Model            string             `json:"model"`
	Messages         []Message          `json:"messages"`
	Temperature      *float32           `json:"temperature,omitempty"`
	MaxTokens        *int               `json:"max_tokens,omitempty"`
	TopP             *float32           `json:"top_p,omitempty"`
	N                *int               `json:"n,omitempty"`
	Stop             any                `json:"stop,omitempty"`
	PresencePenalty  *float32           `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float32           `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]float32 `json:"logit_bias,omitempty"`
	User             *string            `json:"user,omitempty"`
	Seed             *int               `json:"seed,omitempty"`
	Tools            []any              `json:"tools,omitempty"`
	ToolChoice       any                `json:"tool_choice,omitempty"`
	ResponseFormat   any                `json:"response_format,omitempty"`
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// EmbeddingRequest represents an embedding request
type EmbeddingRequest struct {
	Model          string  `json:"model"`
	Input          any     `json:"input"`
	EncodingFormat *string `json:"encoding_format,omitempty"`
	Dimensions     *int    `json:"dimensions,omitempty"`
	User           *string `json:"user,omitempty"`
}

// ChatCompletionResponse represents a cached chat completion response
type ChatCompletionResponse struct {
	ID                string              `json:"id"`
	Object            string              `json:"object"`
	Created           int64               `json:"created"`
	Model             string              `json:"model"`
	Choices           []connectors.Choice `json:"choices"`
	Usage             *connectors.Usage   `json:"usage,omitempty"`
	SystemFingerprint string              `json:"system_fingerprint,omitempty"`
	ModelUsed         string              `json:"model_used,omitempty"`

	// Cache metadata (not part of the API response)
	CachedAt int64 `json:"cached_at,omitempty"`
}

// EmbeddingsResponse represents a cached embeddings response
type EmbeddingsResponse struct {
	Object string                 `json:"object"`
	Data   []connectors.Embedding `json:"data"`
	Model  string                 `json:"model"`
	Usage  *connectors.Usage      `json:"usage"`

	// Cache metadata (not part of the API response)
	CachedAt int64 `json:"cached_at,omitempty"`
}
