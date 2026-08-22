// Package proxy provides the core business logic for LLM request proxying
package proxy

import (
	"context"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/byok"
)

// Proxy defines the core proxy interface for LLM request handling.
// This interface serves three primary purposes:
// 1. Testing - allows easy mocking in unit tests
// 2. Middleware composition - enables wrapping with caching, logging, metrics, etc.
// 3. Dependency injection - provides clean separation for HTTP handlers
//
// The proxy handles routing requests to appropriate LLM providers,
// managing fallbacks, and transforming between different API formats.
type Proxy interface {
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

// ChatCompletionRequest is a canonical chat request plus gateway policy and identity.
type ChatCompletionRequest struct {
	Request  inference.ChatRequest
	Route    string
	Provider *ProviderPreferences
	// Preset names a stored preset selected by the request body. A
	// "@preset/<name>" model reference selects one the same way and wins
	// over this field.
	Preset string

	// Internal fields
	APIKey       string               `json:"-"`
	TenantID     string               `json:"-"`
	APIKeyConfig *APIKeyRoutingConfig `json:"-"`
	RequestID    string               `json:"-"`
	Protocol     string               `json:"-"` // Protocol surface that received the request
}

// ChatCompletionResponse is a canonical result plus gateway response metadata.
type ChatCompletionResponse struct {
	Response inference.ChatResponse

	// Internal fields (not serialized)
	CacheStatus string     `json:"-"`
	CacheAge    int        `json:"-"` // Seconds since cached
	ETag        string     `json:"-"` // Entity tag for response
	CacheCost   *CacheCost `json:"-"` // Cache pricing information

	// Route evidence (not serialized)
	ProviderUsed    string                           `json:"-"`
	Attempts        int                              `json:"-"`
	RoutingDuration time.Duration                    `json:"-"`
	CatalogSnapshot *runtimecatalog.RoutableSnapshot `json:"-"`
}

// ChatCompletionStreamResponse represents a streaming response
type ChatCompletionStreamResponse interface {
	// Read returns the next canonical event or io.EOF when done.
	Read() (*inference.StreamEvent, error)

	// Close releases any resources
	Close() error
}

// StreamUnwrapper exposes the inner stream of a decorating stream wrapper so
// cross-cutting middleware can reach route evidence on the routed stream.
type StreamUnwrapper interface {
	Unwrap() ChatCompletionStreamResponse
}

// EmbeddingsRequest is a canonical embedding request plus gateway identity.
type EmbeddingsRequest struct {
	Request inference.EmbeddingRequest

	// Internal fields
	APIKey       string               `json:"-"`
	TenantID     string               `json:"-"`
	APIKeyConfig *APIKeyRoutingConfig `json:"-"`
	RequestID    string               `json:"-"`
	Protocol     string               `json:"-"` // Protocol surface that received the request
}

// EmbeddingsResponse is a canonical embedding result plus gateway metadata.
type EmbeddingsResponse struct {
	Response inference.EmbeddingResponse

	// Internal fields (not serialized)
	CacheStatus string     `json:"-"`
	CacheAge    int        `json:"-"` // Seconds since cached
	ETag        string     `json:"-"` // Entity tag for response
	CacheCost   *CacheCost `json:"-"` // Cache pricing information

	// Route evidence (not serialized)
	ModelUsed       string                           `json:"-"`
	ProviderUsed    string                           `json:"-"`
	Attempts        int                              `json:"-"`
	RoutingDuration time.Duration                    `json:"-"`
	CatalogSnapshot *runtimecatalog.RoutableSnapshot `json:"-"`
}

// ModelsResponse represents a list of available models
type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`

	// Internal fields (not serialized)
	CacheStatus string `json:"-"`
}

// The console- and API-facing catalog DTOs live in
// internal/catalog/view; the aliases keep this package's public surface
// stable while the proxy stays a thin caller of the projection seam.
type (
	// ModelInfo represents model information
	ModelInfo = view.ModelInfo
	// ModelPricing represents model pricing information
	ModelPricing = view.ModelPricing
	// ModelArchitecture describes protocol-facing model capabilities.
	ModelArchitecture = view.ModelArchitecture
	// TopProviderInfo describes the selected representative offering limits.
	TopProviderInfo = view.TopProviderInfo
	// ProviderInfo represents provider metadata
	ProviderInfo = view.ProviderInfo
	// CredentialFieldInfo is the catalog-declared inference credential
	// field a caller supplies for BYOK. It carries no secret values.
	CredentialFieldInfo = view.CredentialFieldInfo
	// EndpointInfo represents an endpoint that can serve a model
	EndpointInfo = view.EndpointInfo
)

// ProvidersResponse represents provider information
type ProvidersResponse struct {
	Providers []ProviderInfo `json:"providers"`

	// Internal fields (not serialized)
	CacheStatus string `json:"-"`
}

// ModelEndpointsResponse represents available endpoints for a model
type ModelEndpointsResponse struct {
	Model     string         `json:"model"`
	Endpoints []EndpointInfo `json:"endpoints"`
}

// ProviderPreferences represents provider routing preferences
type ProviderPreferences struct {
	Order         []string `json:"order,omitempty"`
	Ignore        []string `json:"ignore,omitempty"`
	Only          []string `json:"only,omitempty"`
	AllowFallback bool     `json:"allow_fallback,omitempty"`

	// Sort selects route ordering: "price", "latency", or "throughput"
	// (routed by measured latency). Empty keeps the server default.
	Sort string `json:"sort,omitempty"`

	// MaxPromptPricePer1M and MaxCompletionPricePer1M cap the accepted route
	// price in USD per million tokens. Zero means no cap.
	MaxPromptPricePer1M     float64 `json:"max_prompt_price_per_1m,omitempty"`
	MaxCompletionPricePer1M float64 `json:"max_completion_price_per_1m,omitempty"`
}

// APIKeyRoutingConfig contains API-key scoped routing restrictions.
type APIKeyRoutingConfig struct {
	AllowedProviders   []string
	AllowedModels      []string
	ModelOverrides     map[string]string
	RateLimitTier      string
	CredentialStrategy byok.Strategy
}

// CacheCost represents the cost of cache operations
type CacheCost struct {
	WriteTokens float64 `json:"write_tokens"` // Cost of writing tokens to cache
	ReadTokens  float64 `json:"read_tokens"`  // Cost of reading tokens from cache
	TotalCost   float64 `json:"total_cost"`   // Total cost including cache operations
}

// CacheStatusProvider is an interface to check cache status on streams
type CacheStatusProvider interface {
	GetCacheStatus() string
	GetCacheAge() int // Returns cache age in seconds, or 0 if not cached
}

// CacheConfig defines caching behavior
type CacheConfig struct {
	// Enable caching for different endpoints
	EnableChatCache      bool `env:"ENABLE_CHAT_CACHE,default=true"`
	EnableEmbeddingCache bool `env:"ENABLE_EMBEDDING_CACHE,default=true"`
	EnableModelCache     bool `env:"ENABLE_MODEL_CACHE,default=true"`
	EnableProviderCache  bool `env:"ENABLE_PROVIDER_CACHE,default=true"`
	// Skip cache for specific models or patterns
	SkipCacheModels []string `env:"SKIP_CACHE_MODELS"`
	// Force cache refresh header
	CacheControlHeader string `env:"CACHE_CONTROL_HEADER,default=X-Cache-Control"`
}

// Define typed context keys
type contextKey string

const (
	// CacheStatusKey is the context key for cache status
	CacheStatusKey contextKey = "X-Cache"

	// CacheStatusHit indicates a cache hit
	CacheStatusHit = "HIT"
	// CacheStatusMiss indicates a cache miss
	CacheStatusMiss = "MISS"
)
