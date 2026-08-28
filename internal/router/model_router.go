// Package router provides model routing and fallback capabilities for the Starport gateway.
package router

import (
	"context"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/routing"
)

// ModelRouter handles model selection, fallback logic, and provider routing
type ModelRouter interface {
	// SelectModel chooses the best model based on the request and routing strategy
	// Returns the selected model ID and the connector to use
	SelectModel(ctx context.Context, req *Request) (modelID string, connector connectors.Connector, err error)

	// RouteWithFallback attempts to route a request through multiple models with fallback logic
	// Returns the response and which model was actually used
	RouteWithFallback(ctx context.Context, req *Request) (*Response, error)

	// RouteStream executes the same immutable route plan and budget for streaming.
	RouteStream(ctx context.Context, req *Request) (execution.ManagedStream, error)

	// RouteEmbeddings executes one embedding request through the same route,
	// credential, availability, and total-attempt policies as chat requests.
	RouteEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)

	// RouteRerank scores one document list against a query through the same
	// route, credential, availability, and total-attempt policies as chat.
	RouteRerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error)

	// RouteImages executes one image generation or image edit request.
	RouteImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error)

	// RouteSpeech executes one text-to-speech request.
	RouteSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error)

	// RouteTranscription executes one speech-to-text request, in the spoken
	// language or translated into English.
	RouteTranscription(ctx context.Context, req *TranscriptionRequest) (*TranscriptionResponse, error)

	// RouteVideoSubmit starts one video generation at a provider that serves
	// it, and answers with the provider's own job identifier.
	RouteVideoSubmit(ctx context.Context, req *VideoSubmitRequest) (*VideoJobResponse, error)

	// RouteVideoPoll asks the provider that accepted a job where it got to.
	RouteVideoPoll(ctx context.Context, req *VideoJobRequest) (*VideoJobResponse, error)

	// RouteVideoCancel asks the provider that accepted a job to stop it.
	RouteVideoCancel(ctx context.Context, req *VideoJobRequest) (*VideoJobResponse, error)

	// RouteVideoContent reads the finished output of one accepted job from the
	// provider that produced it.
	RouteVideoContent(ctx context.Context, req *VideoAssetRequest) (*VideoAssetResponse, error)

	// RouteDocumentRecognition reads the text off a document whose pages carry
	// none. The gateway orders this read on a caller's behalf inside a chat
	// turn, so it is the one route no HTTP path reaches.
	RouteDocumentRecognition(ctx context.Context, req *RecognitionRequest) (*RecognitionResponse, error)
}

// Request contains the original request plus routing preferences
type Request struct {
	// Original chat request
	*connectors.ChatRequest

	// Models to try in order (fallback chain)
	Models []string

	// Provider routing preferences
	ProviderPreferences *ProviderPreferences

	// API key configuration (for provider restrictions)
	APIKeyConfig *APIKeyConfig

	// TenantID selects the account whose BYOK credential record the request
	// may read. It is never a gateway API key ID.
	TenantID string

	// Request metadata for routing decisions
	Metadata *RequestMetadata

	// PrepareAttempt optionally adjusts the provider request for the selected
	// model immediately before invoking the connector.
	PrepareAttempt func(route routing.Route, req *connectors.ChatRequest) *connectors.ChatRequest
}

// ProviderPreferences controls which providers can be used
type ProviderPreferences struct {
	// Try providers in this order
	Order []string `json:"order,omitempty"`

	// Only use these providers
	Only []string `json:"only,omitempty"`

	// Never use these providers
	Ignore []string `json:"ignore,omitempty"`

	// Allow fallback to other providers not in order list
	AllowFallbacks bool `json:"allow_fallbacks,omitempty"`

	// Sort selects the route ordering: "price", "latency", or "throughput".
	// Starport measures latency, not throughput, so "throughput" routes by
	// measured latency. Empty keeps the server default ordering.
	Sort string `json:"sort,omitempty"`

	// MaxPromptPricePer1M and MaxCompletionPricePer1M cap the accepted route
	// price in USD per million tokens. Zero means no cap.
	MaxPromptPricePer1M     float64 `json:"max_prompt_price_per_1m,omitempty"`
	MaxCompletionPricePer1M float64 `json:"max_completion_price_per_1m,omitempty"`
}

// APIKeyConfig contains API key specific routing configuration
type APIKeyConfig struct {
	// Allowed providers for this API key
	AllowedProviders []string

	// Allowed models for this API key
	AllowedModels []string

	// Model-specific overrides
	ModelOverrides map[string]string

	// Rate limit tier
	RateLimitTier string

	// CredentialStrategy is the effective credential order for this request,
	// already resolved against the account's governing strategy by the caller.
	CredentialStrategy keyring.Strategy
}

// RequestMetadata contains information for routing decisions
type RequestMetadata struct {
	// Estimated tokens in the request
	EstimatedTokens int

	// Required features (e.g., "vision", "function_calling")
	RequiredFeatures []string

	// RequiredModalities names the media the request carries, such as
	// "audio". A model that does not read one of them cannot answer the
	// request, so the planner drops it rather than letting the provider
	// refuse the call.
	RequiredModalities []string

	// Conversation ID for sticky routing
	ConversationID string

	// User preferences
	UserPreferences map[string]any
}

// Response wraps the chat response with routing metadata
type Response struct {
	// The actual response from the model
	*connectors.ChatResponse

	// Which model was actually used
	ModelUsed string `json:"model_used"`

	// Provider that handled the request
	ProviderUsed string `json:"provider_used"`

	// CredentialSource names which credential plane paid for the attempt
	// that answered: the operator's environment, the operator's applied
	// gateway credential, the tenant's own BYOK, or no credential at all.
	CredentialSource string `json:"credential_source,omitempty"`

	// Number of attempts made
	Attempts int `json:"attempts"`

	// Routing metadata
	Metadata *Metadata `json:"metadata,omitempty"`

	// CatalogSnapshot is the exact leased runtime generation that produced the
	// response.
	CatalogSnapshot *runtimecatalog.RoutableSnapshot `json:"-"`
}

// EmbeddingRequest contains one provider-neutral embedding request plus
// tenant routing and credential policy.
type EmbeddingRequest struct {
	*connectors.EmbeddingsRequest
	APIKeyConfig *APIKeyConfig
	TenantID     string
}

// EmbeddingResponse wraps one embedding result with route evidence.
type EmbeddingResponse struct {
	Response         inference.EmbeddingResponse
	ModelUsed        string
	ProviderUsed     string
	CredentialSource string
	Attempts         int
	Metadata         *Metadata
	CatalogSnapshot  *runtimecatalog.RoutableSnapshot
}

// Metadata contains detailed routing information
type Metadata struct {
	// Models that were tried
	ModelsAttempted []ModelAttempt `json:"models_attempted"`

	// Total routing time
	RoutingDuration time.Duration `json:"routing_duration_ms"`

	// Reason for final model selection
	SelectionReason string `json:"selection_reason"`
}

// ModelAttempt records an attempt to use a specific model
type ModelAttempt struct {
	Model    string        `json:"model"`
	Provider string        `json:"provider"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration_ms"`
	Status   string        `json:"status"` // "success", "failed", "skipped"
}
