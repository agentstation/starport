package connectors_test

import (
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/connectors"
)

func TestAPIError(t *testing.T) {
	tests := []struct {
		name      string
		err       connectors.APIError
		wantError string
		retryable bool
	}{
		{
			name: "rate limit error",
			err: connectors.APIError{
				Provider:   "openai",
				StatusCode: 429,
				Type:       "rate_limit_exceeded",
				Message:    "Rate limit exceeded",
				Code:       "rate_limit",
			},
			wantError: "openai API error (status 429, code rate_limit): Rate limit exceeded",
			retryable: true,
		},
		{
			name: "server error",
			err: connectors.APIError{
				Provider:   "anthropic",
				StatusCode: 500,
				Message:    "Internal server error",
			},
			wantError: "anthropic API error (status 500): Internal server error",
			retryable: true,
		},
		{
			name: "bad request",
			err: connectors.APIError{
				Provider:   "gemini",
				StatusCode: 400,
				Type:       "invalid_request",
				Message:    "Invalid model specified",
			},
			wantError: "gemini API error (status 400): Invalid model specified",
			retryable: false,
		},
		{
			name: "unauthorized",
			err: connectors.APIError{
				Provider:   "groq",
				StatusCode: 401,
				Message:    "Invalid API key",
				Code:       "unauthorized",
			},
			wantError: "groq API error (status 401, code unauthorized): Invalid API key",
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantError {
				t.Errorf("APIError.Error() = %v, want %v", got, tt.wantError)
			}
			if got := tt.err.IsRetryable(); got != tt.retryable {
				t.Errorf("APIError.IsRetryable() = %v, want %v", got, tt.retryable)
			}
		})
	}
}

func TestStreamError(t *testing.T) {
	baseErr := errors.New("connection reset")

	tests := []struct {
		name      string
		err       connectors.StreamError
		wantError string
	}{
		{
			name: "error with chunk number",
			err: connectors.StreamError{
				Err:    baseErr,
				Chunk:  5,
				Reason: "network failure",
			},
			wantError: "stream error at chunk 5: network failure: connection reset",
		},
		{
			name: "error without chunk number",
			err: connectors.StreamError{
				Err:    baseErr,
				Reason: "unexpected EOF",
			},
			wantError: "stream error: unexpected EOF: connection reset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantError {
				t.Errorf("StreamError.Error() = %v, want %v", got, tt.wantError)
			}

			// Test Unwrap
			if unwrapped := tt.err.Unwrap(); !errors.Is(unwrapped, baseErr) {
				t.Errorf("StreamError.Unwrap() = %v, want %v", unwrapped, baseErr)
			}
		})
	}
}

func TestIsAPIError(t *testing.T) {
	apiErr := &connectors.APIError{
		Provider:   "test",
		StatusCode: 500,
		Message:    "error",
	}

	if !connectors.IsAPIError(apiErr) {
		t.Error("expected IsAPIError to return true for APIError")
	}

	normalErr := errors.New("normal error")
	if connectors.IsAPIError(normalErr) {
		t.Error("expected IsAPIError to return false for non-APIError")
	}

	// Test with wrapped error
	wrappedErr := errors.Join(errors.New("wrapper"), apiErr)
	if !connectors.IsAPIError(wrappedErr) {
		t.Error("expected IsAPIError to return true for wrapped APIError")
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name: "retryable API error",
			err: &connectors.APIError{
				Provider:   "test",
				StatusCode: 429,
				Message:    "rate limited",
			},
			retryable: true,
		},
		{
			name: "non-retryable API error",
			err: &connectors.APIError{
				Provider:   "test",
				StatusCode: 400,
				Message:    "bad request",
			},
			retryable: false,
		},
		{
			name:      "rate limited error",
			err:       connectors.ErrRateLimited,
			retryable: true,
		},
		{
			name:      "timeout error",
			err:       connectors.ErrTimeout,
			retryable: true,
		},
		{
			name:      "invalid API key",
			err:       connectors.ErrInvalidAPIKey,
			retryable: false,
		},
		{
			name:      "normal error",
			err:       errors.New("some error"),
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectors.IsRetryable(tt.err); got != tt.retryable {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.retryable)
			}
		})
	}
}

func TestErrorConstants(t *testing.T) {
	// Test that all error constants are defined
	errorTests := []struct {
		name string
		err  error
		msg  string
	}{
		{"ErrProviderNotSupported", connectors.ErrProviderNotSupported, "provider not supported"},
		{"ErrInvalidConfig", connectors.ErrInvalidConfig, "invalid configuration"},
		{"ErrInvalidMessageContent", connectors.ErrInvalidMessageContent, "invalid message content format"},
		{"ErrStreamClosed", connectors.ErrStreamClosed, "stream closed"},
		{"ErrHealthCheckFailed", connectors.ErrHealthCheckFailed, "health check failed"},
		{"ErrRateLimited", connectors.ErrRateLimited, "rate limited"},
		{"ErrInvalidAPIKey", connectors.ErrInvalidAPIKey, "invalid or missing API key"},
		{"ErrTimeout", connectors.ErrTimeout, "request timeout"},
		{"ErrContextCanceled", connectors.ErrContextCanceled, "context canceled"},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.msg {
				t.Errorf("expected error message %q, got %q", tt.msg, tt.err.Error())
			}
		})
	}
}
