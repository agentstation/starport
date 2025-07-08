// Package connectors provides interfaces and types for LLM provider integrations
package connectors

import (
	"errors"
	"fmt"
)

// Common errors
var (
	// ErrProviderNotSupported indicates the provider is not supported
	ErrProviderNotSupported = errors.New("provider not supported")

	// ErrInvalidConfig indicates invalid configuration
	ErrInvalidConfig = errors.New("invalid configuration")

	// ErrInvalidMessageContent indicates invalid message content format
	ErrInvalidMessageContent = errors.New("invalid message content format")

	// ErrStreamClosed indicates the stream has been closed
	ErrStreamClosed = errors.New("stream closed")

	// ErrHealthCheckFailed indicates health check failed
	ErrHealthCheckFailed = errors.New("health check failed")

	// ErrRateLimited indicates the provider returned a rate limit error
	ErrRateLimited = errors.New("rate limited")

	// ErrInvalidAPIKey indicates invalid or missing API key
	ErrInvalidAPIKey = errors.New("invalid or missing API key")

	// ErrTimeout indicates a request timeout
	ErrTimeout = errors.New("request timeout")

	// ErrContextCanceled indicates the context was canceled
	ErrContextCanceled = errors.New("context canceled")
)

// APIError represents an error from the provider's API
type APIError struct {
	Provider   string `json:"provider"`
	StatusCode int    `json:"status_code"`
	Type       string `json:"type,omitempty"`
	Message    string `json:"message"`
	Code       string `json:"code,omitempty"`
}

// Error implements the error interface
func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s API error (status %d, code %s): %s", e.Provider, e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("%s API error (status %d): %s", e.Provider, e.StatusCode, e.Message)
}

// IsRetryable returns true if the error is retryable
func (e *APIError) IsRetryable() bool {
	switch e.StatusCode {
	case 429: // Rate limited
		return true
	case 500, 502, 503, 504: // Server errors
		return true
	default:
		return false
	}
}

// StreamError represents an error during streaming
type StreamError struct {
	Err    error
	Chunk  int
	Reason string
}

// Error implements the error interface
func (e *StreamError) Error() string {
	if e.Chunk > 0 {
		return fmt.Sprintf("stream error at chunk %d: %s: %v", e.Chunk, e.Reason, e.Err)
	}
	return fmt.Sprintf("stream error: %s: %v", e.Reason, e.Err)
}

// Unwrap returns the underlying error
func (e *StreamError) Unwrap() error {
	return e.Err
}

// IsAPIError checks if an error is an APIError
func IsAPIError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr)
}

// IsRetryable checks if an error is retryable
func IsRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsRetryable()
	}

	// Check for specific errors that are retryable
	return errors.Is(err, ErrRateLimited) || errors.Is(err, ErrTimeout)
}

// NewAPIError creates a new APIError with the given status code and message
func NewAPIError(statusCode int, message string) *APIError {
	return &APIError{
		StatusCode: statusCode,
		Message:    message,
	}
}
