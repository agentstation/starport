package providers

import "errors"

// Provider-related errors
var (
	// ErrProviderNotFound is returned when a provider cannot be found
	ErrProviderNotFound = errors.New("provider not found")

	// ErrProviderDisabled is returned when attempting to use a disabled provider
	ErrProviderDisabled = errors.New("provider is disabled")

	// ErrProviderNotReady is returned when a provider is not properly configured
	ErrProviderNotReady = errors.New("provider is not ready")

	// ErrNoConnector is returned when a provider has no connector configured
	ErrNoConnector = errors.New("no connector configured")
)

// Model-related errors
var (
	// ErrModelNotFound is returned when a model cannot be found
	ErrModelNotFound = errors.New("model not found")

	// ErrInvalidModelType is returned when a model is used for the wrong operation
	ErrInvalidModelType = errors.New("invalid model type for operation")

	// ErrModelDeprecated is returned when attempting to use a deprecated model
	ErrModelDeprecated = errors.New("model is deprecated")

	// ErrModelNotSupported is returned when a model is not supported by the provider
	ErrModelNotSupported = errors.New("model not supported by provider")
)

// Configuration errors
var (
	// ErrMissingAPIKey is returned when an API key is required but not provided
	ErrMissingAPIKey = errors.New("missing API key")

	// ErrInvalidBaseURL is returned when the base URL is invalid
	ErrInvalidBaseURL = errors.New("invalid base URL")

	// ErrInvalidConfig is returned when configuration is invalid
	ErrInvalidConfig = errors.New("invalid configuration")
)

// Operation errors
var (
	// ErrNotImplemented is returned when an operation is not implemented by a connector
	ErrNotImplemented = errors.New("operation not implemented")

	// ErrStreamNotSupported is returned when streaming is not supported
	ErrStreamNotSupported = errors.New("streaming not supported for this model")

	// ErrContextCanceled is returned when the context is canceled
	ErrContextCanceled = errors.New("context canceled")

	// ErrTimeout is returned when an operation times out
	ErrTimeout = errors.New("operation timed out")
)

// API errors
var (
	// ErrRateLimitExceeded is returned when rate limits are exceeded
	ErrRateLimitExceeded = errors.New("rate limit exceeded")

	// ErrQuotaExceeded is returned when quota is exceeded
	ErrQuotaExceeded = errors.New("quota exceeded")

	// ErrInvalidRequest is returned when the request is invalid
	ErrInvalidRequest = errors.New("invalid request")

	// ErrUnauthorized is returned when authentication fails
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden is returned when access is forbidden
	ErrForbidden = errors.New("forbidden")

	// ErrServerError is returned when the server encounters an error
	ErrServerError = errors.New("server error")
)

// APIError represents a detailed API error
type APIError struct {
	Provider   string `json:"provider"`
	StatusCode int    `json:"status_code"`
	Type       string `json:"type,omitempty"`
	Message    string `json:"message"`
	Details    string `json:"details,omitempty"`
}

// Error implements the error interface
func (e *APIError) Error() string {
	if e.Details != "" {
		return e.Message + ": " + e.Details
	}
	return e.Message
}

// IsRetryable returns true if the error is retryable
func (e *APIError) IsRetryable() bool {
	// 5xx errors are generally retryable
	if e.StatusCode >= 500 {
		return true
	}
	// 429 (rate limit) is retryable
	if e.StatusCode == 429 {
		return true
	}
	return false
}

// StreamError represents an error during streaming
type StreamError struct {
	Provider string `json:"provider"`
	Message  string `json:"message"`
	Err      error  `json:"-"`
}

// Error implements the error interface
func (e *StreamError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap returns the underlying error
func (e *StreamError) Unwrap() error {
	return e.Err
}

// ValidationError represents a validation error
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface
func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// MultiError represents multiple errors
type MultiError struct {
	Errors []error `json:"errors"`
}

// Error implements the error interface
func (e *MultiError) Error() string {
	if len(e.Errors) == 0 {
		return "no errors"
	}
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	return "multiple errors occurred"
}

// Add adds an error to the collection
func (e *MultiError) Add(err error) {
	if err != nil {
		e.Errors = append(e.Errors, err)
	}
}

// HasErrors returns true if there are any errors
func (e *MultiError) HasErrors() bool {
	return len(e.Errors) > 0
}

// First returns the first error or nil
func (e *MultiError) First() error {
	if len(e.Errors) > 0 {
		return e.Errors[0]
	}
	return nil
}