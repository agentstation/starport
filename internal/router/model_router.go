// package router provides model routing and fallback capabilities for the Starport gateway
package router

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
)

// ModelRouter handles model selection, fallback logic, and provider routing
type ModelRouter interface {
	// SelectModel chooses the best model based on the request and routing strategy
	// Returns the selected model ID and the connector to use
	SelectModel(ctx context.Context, req *Request) (modelID string, connector connectors.Connector, err error)

	// RouteWithFallback attempts to route a request through multiple models with fallback logic
	// Returns the response and which model was actually used
	RouteWithFallback(ctx context.Context, req *Request) (*Response, error)
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

	// Request metadata for routing decisions
	Metadata *RequestMetadata
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
}

// APIKeyConfig contains API key specific routing configuration
type APIKeyConfig struct {
	// Allowed providers for this API key
	AllowedProviders []string

	// Model-specific overrides
	ModelOverrides map[string]string

	// Rate limit tier
	RateLimitTier string
}

// RequestMetadata contains information for routing decisions
type RequestMetadata struct {
	// Estimated tokens in the request
	EstimatedTokens int

	// Required features (e.g., "vision", "function_calling")
	RequiredFeatures []string

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

	// Number of attempts made
	Attempts int `json:"attempts"`

	// Routing metadata
	Metadata *Metadata `json:"metadata,omitempty"`
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

// FallbackTrigger represents reasons to trigger fallback
type FallbackTrigger int

const (
	// FallbackNone indicates no fallback needed
	FallbackNone FallbackTrigger = iota
	// FallbackRateLimit triggered by 429 error
	FallbackRateLimit
	// FallbackModelUnavailable triggered by 404 or model not found
	FallbackModelUnavailable
	// FallbackContextExceeded triggered when context length is exceeded
	FallbackContextExceeded
	// FallbackProviderError triggered by 5xx errors
	FallbackProviderError
	// FallbackContentModeration triggered by content policy violations
	FallbackContentModeration
	// FallbackTimeout triggered by request timeout
	FallbackTimeout
)

// IsFallbackError determines if an error should trigger fallback
func IsFallbackError(err error) (FallbackTrigger, bool) {
	if err == nil {
		return FallbackNone, false
	}

	// Check if it's an API error
	if apiErr, ok := err.(*connectors.APIError); ok {
		switch apiErr.StatusCode {
		case 429:
			return FallbackRateLimit, true
		case 404:
			return FallbackModelUnavailable, true
		case 400:
			// Check for context length errors
			if containsContextError(apiErr.Message) {
				return FallbackContextExceeded, true
			}
			// Check for content moderation
			if containsContentError(apiErr.Message) {
				return FallbackContentModeration, true
			}
		case 500, 502, 503, 504:
			return FallbackProviderError, true
		}
	}

	// Check for timeout
	if isTimeoutError(err) {
		return FallbackTimeout, true
	}

	return FallbackNone, false
}

func containsContextError(msg string) bool {
	// Common context length error patterns
	contextErrors := []string{
		"context length",
		"context_length_exceeded",
		"max_tokens",
		"token limit",
		"maximum context",
	}
	for _, pattern := range contextErrors {
		if contains(msg, pattern) {
			return true
		}
	}
	return false
}

func containsContentError(msg string) bool {
	// Common content moderation error patterns
	contentErrors := []string{
		"content_policy",
		"content moderation",
		"safety",
		"harmful",
		"inappropriate",
	}
	for _, pattern := range contentErrors {
		if contains(msg, pattern) {
			return true
		}
	}
	return false
}

func isTimeoutError(err error) bool {
	// Check for context deadline exceeded or timeout errors
	errStr := err.Error()
	return contains(errStr, "timeout") || contains(errStr, "deadline exceeded")
}

// Simple case-insensitive contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsIgnoreCase(s, substr)
}

func containsIgnoreCase(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	// Simple implementation - in production would use strings.Contains with lowercasing
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if toLower(s[i+j]) != toLower(substr[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func toLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}
