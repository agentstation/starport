package failure

import (
	"errors"
	"strings"
	"testing"
)

func TestFailureSeparatesSafeAndProviderDetails(t *testing.T) {
	cause := errors.New("internal transport cause")
	failure := New(
		RateLimit,
		"The provider rate limit was reached.",
		true,
		ProviderDetails{
			Provider:   "example",
			StatusCode: 429,
			Code:       "capacity_exhausted",
			Message:    "provider-only diagnostic token",
			StateScope: ScopeOffering,
		},
		cause,
	)

	if strings.Contains(failure.Error(), "provider-only") {
		t.Fatal("safe error exposed provider details")
	}
	if failure.ProviderDetails().Message != "provider-only diagnostic token" {
		t.Fatal("provider evidence was lost")
	}
	if failure.Kind() != RateLimit || !failure.Retryable() || !errors.Is(failure, cause) {
		t.Fatal("normalized failure semantics changed")
	}
	if failure.StateScope() != ScopeOffering {
		t.Fatal("provider state scope was lost")
	}
}

func TestFailureWithoutScopeCannotChangeDurableState(t *testing.T) {
	providerFailure := New(
		Permission,
		"The provider denied the request.",
		false,
		ProviderDetails{Provider: "example"},
		nil,
	)
	if providerFailure.StateScope() != ScopeNone {
		t.Fatal("unscoped provider evidence became durable")
	}
}
