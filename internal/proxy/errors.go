package proxy

import (
	"errors"
	"fmt"
)

// Common proxy errors
var (
	// ErrNoValidModel indicates no valid model was specified
	ErrNoValidModel = errors.New("no valid model specified")

	// ErrNoAvailableProvider indicates no provider is available for the request
	ErrNoAvailableProvider = errors.New("no available provider for model")

	// ErrInvalidRequest indicates the request is malformed
	ErrInvalidRequest = errors.New("invalid request")

	// ErrProviderUnavailable indicates the provider is temporarily unavailable
	ErrProviderUnavailable = errors.New("provider temporarily unavailable")

	// ErrRateLimitExceeded indicates rate limit has been exceeded
	ErrRateLimitExceeded = errors.New("rate limit exceeded")

	// ErrInsufficientQuota indicates the user has insufficient quota
	ErrInsufficientQuota = errors.New("insufficient quota")

	// ErrStreamingNotSupported indicates streaming is not supported for this request
	ErrStreamingNotSupported = errors.New("streaming not supported")

	// ErrEmbeddingsNotSupported indicates embeddings are not supported by the provider
	ErrEmbeddingsNotSupported = errors.New("embeddings not supported by provider")

	// ErrPresetNotFound indicates a request referenced an unknown preset
	ErrPresetNotFound = errors.New("preset not found")
)

// ValidationError represents a request validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
}

// ProviderError represents an error from a provider
type ProviderError struct {
	Provider string
	Code     string
	Message  string
	Err      error
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("provider %s error: %s: %s (%v)", e.Provider, e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("provider %s error: %s: %s", e.Provider, e.Code, e.Message)
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// RoutingError represents an error during model routing
type RoutingError struct {
	Model  string
	Reason string
	Err    error
}

func (e *RoutingError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("routing error for model %s: %s: %v", e.Model, e.Reason, e.Err)
	}
	return fmt.Sprintf("routing error for model %s: %s", e.Model, e.Reason)
}

func (e *RoutingError) Unwrap() error {
	return e.Err
}
