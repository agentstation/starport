// Package proxy provides the core business logic for LLM request proxying
package proxy

import (
	"context"

	"github.com/agentstation/starport/internal/providers/connectors"
)

// Service defines the core proxy service interface
type Service interface {
	// ProcessChatCompletion handles chat completion requests with routing and processing
	ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)

	// ProcessChatCompletionStream handles streaming chat completion requests
	ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error)

	// ProcessEmbeddings handles embedding generation requests
	ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error)

	// ListModels returns available models based on routing configuration
	ListModels(ctx context.Context) (*ModelsResponse, error)

	// ListProviders returns available provider information
	ListProviders(ctx context.Context) (*ProvidersResponse, error)

	// GetModelEndpoints returns provider endpoints for a specific model
	GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error)
}

// ChatCompletionRequest represents a chat completion request
type ChatCompletionRequest struct {
	// OpenAI-compatible fields
	Model            string                     `json:"model,omitempty"`
	Messages         []connectors.Message       `json:"messages"`
	Temperature      *float32                   `json:"temperature,omitempty"`
	TopP             *float32                   `json:"top_p,omitempty"`
	N                *int                       `json:"n,omitempty"`
	Stream           bool                       `json:"stream,omitempty"`
	Stop             []string                   `json:"stop,omitempty"`
	MaxTokens        *int                       `json:"max_tokens,omitempty"`
	PresencePenalty  *float32                   `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float32                   `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]float32         `json:"logit_bias,omitempty"`
	User             string                     `json:"user,omitempty"`
	Seed             *int                       `json:"seed,omitempty"`
	Tools            []connectors.Tool          `json:"tools,omitempty"`
	ToolChoice       interface{}                `json:"tool_choice,omitempty"`
	ResponseFormat   *connectors.ResponseFormat `json:"response_format,omitempty"`

	// OpenRouter-compatible fields
	Models    []string               `json:"models,omitempty"`
	Route     string                 `json:"route,omitempty"`
	Provider  *ProviderPreferences   `json:"provider,omitempty"`
	Reasoning *ReasoningConfig       `json:"reasoning,omitempty"`

	// Internal fields
	APIKey    string `json:"-"`
	RequestID string `json:"-"`
}

// ChatCompletionResponse represents a chat completion response
type ChatCompletionResponse struct {
	ID                string              `json:"id"`
	Object            string              `json:"object"`
	Created           int64               `json:"created"`
	Model             string              `json:"model"`
	Choices           []connectors.Choice `json:"choices"`
	Usage             *connectors.Usage   `json:"usage,omitempty"`
	SystemFingerprint string              `json:"system_fingerprint,omitempty"`

	// OpenRouter-compatible fields
	ModelUsed string `json:"model_used,omitempty"`

	// Internal fields (not serialized)
	CacheStatus string `json:"-"`
}

// ChatCompletionStreamResponse represents a streaming response
type ChatCompletionStreamResponse interface {
	// Read returns the next chunk or io.EOF when done
	Read() (*connectors.ChatStreamChunk, error)

	// Close releases any resources
	Close() error
}

// EmbeddingsRequest represents an embeddings request
type EmbeddingsRequest struct {
	Model          string      `json:"model"`
	Input          interface{} `json:"input"`
	EncodingFormat string      `json:"encoding_format,omitempty"`
	Dimensions     *int        `json:"dimensions,omitempty"`
	User           string      `json:"user,omitempty"`

	// Internal fields
	APIKey    string `json:"-"`
	RequestID string `json:"-"`
}

// EmbeddingsResponse represents an embeddings response
type EmbeddingsResponse struct {
	Object string                 `json:"object"`
	Data   []connectors.Embedding `json:"data"`
	Model  string                 `json:"model"`
	Usage  *connectors.Usage      `json:"usage,omitempty"`

	// Internal fields (not serialized)
	CacheStatus string `json:"-"`
}

// ModelsResponse represents a list of available models
type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`

	// Internal fields (not serialized)
	CacheStatus string `json:"-"`
}

// ModelInfo represents model information
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`

	// Extended metadata for OpenRouter compatibility
	Pricing     *ModelPricing `json:"pricing,omitempty"`
	Context     *int          `json:"context_length,omitempty"`
	Type        string        `json:"type,omitempty"`
	Description string        `json:"description,omitempty"`
}

// ModelPricing represents model pricing information
type ModelPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
	Currency   string `json:"currency"`
}

// ProvidersResponse represents provider information
type ProvidersResponse struct {
	Providers []ProviderInfo `json:"providers"`

	// Internal fields (not serialized)
	CacheStatus string `json:"-"`
}

// ProviderInfo represents provider metadata
type ProviderInfo struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	URL             string   `json:"url,omitempty"`
	Models          []string `json:"models"`
	Capabilities    []string `json:"capabilities,omitempty"`
	RequiresAuth    bool     `json:"requires_auth"`
	AuthDescription string   `json:"auth_description,omitempty"`
}

// ModelEndpointsResponse represents available endpoints for a model
type ModelEndpointsResponse struct {
	Model     string         `json:"model"`
	Endpoints []EndpointInfo `json:"endpoints"`
}

// EndpointInfo represents an endpoint that can serve a model
type EndpointInfo struct {
	Provider   string `json:"provider"`
	Endpoint   string `json:"endpoint"`
	Available  bool   `json:"available"`
	Latency    *int   `json:"latency_ms,omitempty"`
	CostPrompt string `json:"cost_prompt,omitempty"`
	CostOutput string `json:"cost_output,omitempty"`
}

// ProviderPreferences represents provider routing preferences
type ProviderPreferences struct {
	Order         []string `json:"order,omitempty"`
	Ignore        []string `json:"ignore,omitempty"`
	Only          []string `json:"only,omitempty"`
	AllowFallback bool     `json:"allow_fallback,omitempty"`
}

// ReasoningConfig represents OpenRouter-style reasoning configuration
type ReasoningConfig struct {
	Effort    string `json:"effort,omitempty"`     // "high", "medium", "low"
	MaxTokens *int   `json:"max_tokens,omitempty"` // Alternative to effort
	Exclude   bool   `json:"exclude,omitempty"`    // Exclude reasoning from response
}
