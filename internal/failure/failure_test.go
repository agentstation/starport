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
}
