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
