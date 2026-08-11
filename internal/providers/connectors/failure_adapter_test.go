package connectors

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/failure"
)

func TestNormalizeFailureRetainsProviderEvidence(t *testing.T) {
	providerError := &APIError{
		Provider:   "openai",
		StatusCode: 429,
		Type:       "requests",
		Code:       "rate_limit_exceeded",
		Message:    "provider diagnostic",
	}

	normalized := NormalizeFailure("openai", providerError)
	if normalized.Kind() != failure.RateLimit || !normalized.Retryable() {
		t.Fatal("rate limit semantics were not normalized")
	}
	if strings.Contains(normalized.Error(), providerError.Message) {
		t.Fatal("safe error exposed provider diagnostics")
	}
	if normalized.ProviderDetails().Message != providerError.Message {
		t.Fatal("provider diagnostics were lost")
	}
}

func TestNormalizeFailureClassifiesExecutionPolicyFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		message   string
		wantKind  failure.Kind
		retryable bool
	}{
		{name: "context limit", status: 400, message: "maximum context length exceeded", wantKind: failure.ContextLimit},
		{name: "content blocked", status: 422, message: "content_policy blocked the request", wantKind: failure.ContentBlocked},
		{name: "not found", status: 404, message: "missing", wantKind: failure.NotFound},
		{name: "unavailable", status: 503, message: "down", wantKind: failure.ProviderUnavailable, retryable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized := NormalizeFailure("provider", &APIError{StatusCode: test.status, Message: test.message})
			if normalized.Kind() != test.wantKind || normalized.Retryable() != test.retryable {
				t.Fatalf("got kind %q retryable %t, want kind %q retryable %t", normalized.Kind(), normalized.Retryable(), test.wantKind, test.retryable)
			}
		})
	}
}

func TestNormalizeFailurePreservesCanonicalFailure(t *testing.T) {
	want := failure.New(failure.ContentBlocked, "blocked", false, failure.ProviderDetails{Provider: "provider"}, errors.New("cause"))
	wrapped := fmt.Errorf("adapter context: %w", want)
	if got := NormalizeFailure("other", wrapped); got != want {
		t.Fatal("canonical failure identity changed")
	}
}

func TestNormalizeFailureUsesDocumentedAccountAndOfferingScopes(t *testing.T) {
	tests := []struct {
		name      string
		apiError  *APIError
		wantKind  failure.Kind
		wantScope failure.StateScope
	}{
		{
			name:     "anthropic billing",
			apiError: &APIError{StatusCode: 402, Type: "billing_error", Message: "billing"},
			wantKind: failure.Billing, wantScope: failure.ScopeCredential,
		},
		{
			name:     "openai credits",
			apiError: &APIError{StatusCode: 429, Code: "credit_balance_exhausted", Message: "credits"},
			wantKind: failure.Billing, wantScope: failure.ScopeCredential,
		},
		{
			name:     "explicit quota",
			apiError: &APIError{StatusCode: 429, Code: "insufficient_quota", Message: "quota"},
			wantKind: failure.Quota, wantScope: failure.ScopeOffering,
		},
		{
			name:     "generic rate limit",
			apiError: &APIError{StatusCode: 429, Type: "rate_limit_error", Message: "rate"},
			wantKind: failure.RateLimit, wantScope: failure.ScopeOffering,
		},
		{
			name:     "documented permission",
			apiError: &APIError{StatusCode: 403, Type: "permission_error", Message: "permission"},
			wantKind: failure.Permission, wantScope: failure.ScopeCredential,
		},
		{
			name:     "ambiguous permission",
			apiError: &APIError{StatusCode: 403, Message: "permission"},
			wantKind: failure.Permission, wantScope: failure.ScopeNone,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			providerFailure := NormalizeFailure("provider", test.apiError)
			if providerFailure.Kind() != test.wantKind || providerFailure.StateScope() != test.wantScope {
				t.Fatalf(
					"got kind %q scope %q, want kind %q scope %q",
					providerFailure.Kind(), providerFailure.StateScope(),
					test.wantKind, test.wantScope,
				)
			}
		})
	}
}
