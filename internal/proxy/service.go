// Package proxy provides the core business logic for LLM request proxying
package proxy

import (
	"context"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/usage"
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

	// ProcessRerank scores one document list against a query and answers with
	// the caller's own document positions in ranked order.
	ProcessRerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error)

	// ProcessModerations classifies each input against a provider's harm
	// categories and answers with a verdict and a score per category.
	ProcessModerations(ctx context.Context, req *ModerationRequest) (*ModerationResponse, error)

	// ProcessImages handles image generation and image edit requests.
	ProcessImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error)

	// ProcessSpeech handles text-to-speech requests.
	ProcessSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error)

	// ProcessTranscription handles speech-to-text requests, in the spoken
	// language or translated into English.
	ProcessTranscription(ctx context.Context, req *TranscriptionRequest) (*TranscriptionResponse, error)

	// SubmitVideoJob starts one video generation and answers with the
	// provider's own job identifier. Nothing above this interface stores that
	// identifier except the job record, which never hands it back out.
	SubmitVideoJob(ctx context.Context, req *VideoSubmitRequest) (*VideoJobAnswer, error)

	// PollVideoJob asks the provider that accepted a job where it got to.
	PollVideoJob(ctx context.Context, req *VideoJobRequest) (*VideoJobAnswer, error)

	// CancelVideoJob asks the provider that accepted a job to stop it.
	CancelVideoJob(ctx context.Context, req *VideoJobRequest) (*VideoJobAnswer, error)

	// FetchVideoAsset reads the finished output of one accepted job.
	FetchVideoAsset(ctx context.Context, req *VideoAssetRequest) (*VideoAsset, error)

	// VideoJobRunner returns the provider side of one caller's video jobs,
	// bound to the gateway identity the request carries. The record store
	// drives a job through this value and names no transport itself.
	VideoJobRunner(req *VideoSubmitRequest) jobs.Runner

	// ListModels returns available models based on routing configuration
	ListModels(ctx context.Context) (*ModelsResponse, error)

	// ListProviders returns available provider information
	ListProviders(ctx context.Context) (*ProvidersResponse, error)

	// ListAuthors returns catalog author information
	ListAuthors(ctx context.Context) (*AuthorsResponse, error)

	// GetAuthor returns one catalog author. It reports a not_found
	// provider error for an unknown author ID.
	GetAuthor(ctx context.Context, authorID string) (*AuthorInfo, error)

	// GetModelEndpoints returns provider endpoints for a specific model
	GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error)

	// GetLogo returns the catalog-carried SVG brand mark for one provider
	// or author. It reports a not_found provider error when the catalog
	// carries no bytes for this kind and ID.
	GetLogo(ctx context.Context, kind view.LogoKind, id string) ([]byte, error)
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
	APIKey string `json:"-"`
	// AccountID is the account the request runs under. Credential selection,
	// response cache scope, and account limits read this.
	AccountID string `json:"-"`
	// KeyID is the gateway API key that authenticated the request. Usage
	// attribution and per-key limits read this. Many keys share one account, so
	// the two values are distinct and neither substitutes for the other.
	KeyID string `json:"-"`
	// TeamID is the team the serving key is attributed to, or empty for a
	// teamless key. Usage attribution and the team budget read this.
	TeamID       string               `json:"-"`
	APIKeyConfig *APIKeyRoutingConfig `json:"-"`
	RequestID    string               `json:"-"`
	Protocol     string               `json:"-"` // Protocol surface that received the request
	// BatchID names the batch this request runs inside, or is empty for an
	// online request. The usage record carries it, so an operator can read
	// what one batch spent.
	BatchID string `json:"-"`
}

// ChatCompletionResponse is a canonical result plus gateway response metadata.
type ChatCompletionResponse struct {
	Response inference.ChatResponse

	// ExtractionCached reports that every document this turn attached came
	// back from the extraction cache. It is separate from CacheStatus below,
	// which reports the response cache: a turn can read its attachments from
	// the cache and still call the model, and that is the common case for a
	// conversation about one document.
	ExtractionCached bool `json:"-"`

	// The rest of the document read, as the usage record reports it: which
	// engine ran, how many pages it read each way, which model recognized
	// them, what that cost in integer nano-USD, and how long it took.
	ExtractionEngine   string        `json:"-"`
	ExtractionPages    int           `json:"-"`
	RecognizedPages    int           `json:"-"`
	NativePages        int           `json:"-"`
	ExtractionOffering string        `json:"-"`
	ExtractionNanoUSD  int64         `json:"-"`
	ExtractionUnpriced bool          `json:"-"`
	ExtractionDuration time.Duration `json:"-"`

	// Internal fields (not serialized)
	CacheStatus string     `json:"-"`
	CacheAge    int        `json:"-"` // Seconds since cached
	ETag        string     `json:"-"` // Entity tag for response
	CacheCost   *CacheCost `json:"-"` // Cache pricing information

	// Route evidence (not serialized)
	ProviderUsed     string                           `json:"-"`
	CredentialSource string                           `json:"-"`
	Attempts         int                              `json:"-"`
	RoutingDuration  time.Duration                    `json:"-"`
	CatalogSnapshot  *runtimecatalog.RoutableSnapshot `json:"-"`

	// GuardrailVerdict is the strongest verdict the guardrail pipeline
	// answered over this turn: allow, redact, or refuse. Empty means no
	// guardrail ran. The usage record carries it; the wire never does.
	GuardrailVerdict string `json:"-"`
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
	APIKey string `json:"-"`
	// AccountID is the account the request runs under. Credential selection,
	// response cache scope, and account limits read this.
	AccountID string `json:"-"`
	// KeyID is the gateway API key that authenticated the request. Usage
	// attribution and per-key limits read this. Many keys share one account, so
	// the two values are distinct and neither substitutes for the other.
	KeyID string `json:"-"`
	// TeamID is the team the serving key is attributed to, or empty for a
	// teamless key. Usage attribution and the team budget read this.
	TeamID       string               `json:"-"`
	APIKeyConfig *APIKeyRoutingConfig `json:"-"`
	RequestID    string               `json:"-"`
	Protocol     string               `json:"-"` // Protocol surface that received the request
	// BatchID names the batch this request runs inside, or is empty for an
	// online request. The usage record carries it, so an operator can read
	// what one batch spent.
	BatchID string `json:"-"`
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
	ModelUsed        string                           `json:"-"`
	ProviderUsed     string                           `json:"-"`
	CredentialSource string                           `json:"-"`
	Attempts         int                              `json:"-"`
	RoutingDuration  time.Duration                    `json:"-"`
	CatalogSnapshot  *runtimecatalog.RoutableSnapshot `json:"-"`
}

// OperationRequest is one canonical request plus gateway identity. Every
// operation past chat carries the identity a chat request carries, so one type
// states it once.
type OperationRequest[Request any] struct {
	Request Request

	APIKey string `json:"-"`
	// AccountID is the account the request runs under. Credential selection
	// and account limits read this.
	AccountID string `json:"-"`
	// KeyID is the gateway API key that authenticated the request. Usage
	// attribution and per-key limits read this.
	KeyID string `json:"-"`
	// TeamID is the team the serving key is attributed to, or empty for a
	// teamless key. Usage attribution and the team budget read this.
	TeamID       string               `json:"-"`
	APIKeyConfig *APIKeyRoutingConfig `json:"-"`
	RequestID    string               `json:"-"`
	Protocol     string               `json:"-"`
}

// OperationResponse is one canonical result plus gateway route evidence. It
// carries no cache fields. A media answer is not cached, because an image and
// an audio file are large and a caller that repeats a prompt expects a new
// rendering rather than the previous one. A rerank answer is not cached
// either, because its result names positions in one caller's own document
// list and a second caller's list holds different text at those positions.
type OperationResponse[Response any] struct {
	Response Response

	ModelUsed        string                           `json:"-"`
	ProviderUsed     string                           `json:"-"`
	CredentialSource string                           `json:"-"`
	Attempts         int                              `json:"-"`
	RoutingDuration  time.Duration                    `json:"-"`
	CatalogSnapshot  *runtimecatalog.RoutableSnapshot `json:"-"`

	// Cost is what this turn cost, once the accounting middleware has priced it
	// against the snapshot that routed it. It is nil until then, and it stays
	// nil on a turn the catalog could not price. A protocol that names a cost on
	// its usage block reads this field, so the number the caller sees and the
	// number the account is billed come from one derivation.
	Cost *usage.Cost `json:"-"`
}

// MediaRequest routes one canonical media request. The shared carrier is
// OperationRequest.
type MediaRequest[Request any] = OperationRequest[Request]

// MediaResponse is one media result. The shared carrier is OperationResponse:
// reranking is not media, and a media name on the answer it returns would say
// it was.
type MediaResponse[Response any] = OperationResponse[Response]

// RerankRequest is one gateway rerank request.
type RerankRequest = OperationRequest[inference.RerankRequest]

// RerankResponse is one gateway ranked document list.
type RerankResponse = OperationResponse[inference.RerankResponse]

// ModerationRequest is one gateway moderation request.
type ModerationRequest = OperationRequest[inference.ModerationRequest]

// ModerationResponse is one gateway classified input list. It is not cached
// for the same reason a rerank answer is not: the result reads by position
// in one caller's own input list.
type ModerationResponse = OperationResponse[inference.ModerationResponse]

// ImagesRequest is one gateway image generation or image edit request.
type ImagesRequest = OperationRequest[inference.ImagesRequest]

// ImagesResponse is one gateway image result.
type ImagesResponse = OperationResponse[inference.ImagesResponse]

// SpeechRequest is one gateway text-to-speech request.
type SpeechRequest = OperationRequest[inference.SpeechRequest]

// SpeechResponse is one gateway speech result.
type SpeechResponse = OperationResponse[inference.SpeechResponse]

// TranscriptionRequest is one gateway speech-to-text request.
type TranscriptionRequest = OperationRequest[inference.TranscriptionRequest]

// TranscriptionResponse is one gateway transcript.
type TranscriptionResponse = OperationResponse[inference.TranscriptionResponse]

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
	// ModelAuthorInfo names one catalog author of a model.
	ModelAuthorInfo = view.ModelAuthorInfo
	// ModelLineageInfo describes canonical model-family relationships.
	ModelLineageInfo = view.ModelLineageInfo
	// ModelOfferingInfo is one provider's routable offering of a model.
	ModelOfferingInfo = view.ModelOfferingInfo
	// OfferingPricingInfo carries every token price dimension of one offering.
	OfferingPricingInfo = view.OfferingPricingInfo
	// ProviderPolicyInfo summarizes the provider's published data policies.
	ProviderPolicyInfo = view.ProviderPolicyInfo
	// AuthorInfo represents one catalog author or organization.
	AuthorInfo = view.AuthorInfo
)

// AuthorsResponse represents catalog author information
type AuthorsResponse struct {
	Authors []AuthorInfo `json:"authors"`

	// Internal fields (not serialized)
	CacheStatus string `json:"-"`
}

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

	// Sort selects route ordering: "price", "latency", "throughput"
	// (routed by measured latency), or "spread" (weighted balance inside
	// the leading ranking band). Empty keeps the server default.
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
	CredentialStrategy keyring.Strategy

	// Access carries the account's paired provider and model grants. Nil
	// grants every provider and model.
	Access []routing.ProviderAccess

	// BYOKProviders gates which providers the BYOK credential source may
	// serve: nil allows every provider, an empty list none, a non-empty
	// list only its members. It is the account's BYOK policy resolved to
	// plain data, so this package never learns the account vocabulary.
	BYOKProviders *[]string
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
