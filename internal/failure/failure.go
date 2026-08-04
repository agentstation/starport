// Package failure owns normalized inference failure semantics.
package failure

import "fmt"

// Kind identifies a stable failure class.
type Kind string

const (
	// Validation identifies invalid client input.
	Validation Kind = "validation"
	// Authentication identifies invalid credentials.
	Authentication Kind = "authentication"
	// Permission identifies an unauthorized action.
	Permission Kind = "permission"
	// RateLimit identifies provider or gateway throttling.
	RateLimit Kind = "rate_limit"
	// NotFound identifies an absent model or provider resource.
	NotFound Kind = "not_found"
	// ContextLimit identifies input that exceeds a model context window.
	ContextLimit Kind = "context_limit"
	// ContentBlocked identifies a provider content-policy rejection.
	ContentBlocked Kind = "content_blocked"
	// ProviderUnavailable identifies a provider that cannot accept the attempt.
	ProviderUnavailable Kind = "provider_unavailable"
	// Timeout identifies an elapsed request deadline.
	Timeout Kind = "timeout"
	// Canceled identifies caller cancellation.
	Canceled Kind = "canceled"
	// Internal identifies a gateway implementation failure.
	Internal Kind = "internal"
)

// ProviderDetails retains provider evidence for internal policy and diagnostics.
type ProviderDetails struct {
	Provider   string
	StatusCode int
	Type       string
	Code       string
	Message    string
}

// Failure separates a client-safe message from provider and cause details.
type Failure struct {
	kind        Kind
	safeMessage string
	retryable   bool
	provider    ProviderDetails
	cause       error
}

// New constructs one normalized failure.
func New(kind Kind, safeMessage string, retryable bool, provider ProviderDetails, cause error) *Failure {
	return &Failure{
		kind:        kind,
		safeMessage: safeMessage,
		retryable:   retryable,
		provider:    provider,
		cause:       cause,
	}
}

// Error returns only the client-safe failure text.
func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	if f.safeMessage != "" {
		return f.safeMessage
	}
	return fmt.Sprintf("%s failure", f.kind)
}

// Unwrap returns the internal cause for errors.Is and errors.As.
func (f *Failure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.cause
}

// Kind returns the stable failure class.
func (f *Failure) Kind() Kind {
	if f == nil {
		return Internal
	}
	return f.kind
}

// SafeMessage returns text that a protocol adapter can expose.
func (f *Failure) SafeMessage() string {
	if f == nil {
		return ""
	}
	return f.safeMessage
}

// Retryable reports whether policy can retry the logical attempt.
func (f *Failure) Retryable() bool {
	return f != nil && f.retryable
}

// ProviderDetails returns a copy of the provider-only evidence.
func (f *Failure) ProviderDetails() ProviderDetails {
	if f == nil {
		return ProviderDetails{}
	}
	return f.provider
}
