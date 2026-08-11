package byok

import (
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/failure"
	"github.com/stretchr/testify/require"
)

func TestBYOKStrategyOrderAndUserOnlyNoninterference(t *testing.T) {
	tests := []struct {
		strategy Strategy
		want     []CredentialSource
	}{
		{strategy: OperatorFirst, want: []CredentialSource{CredentialSourceOperator, CredentialSourceUser}},
		{strategy: UserFirst, want: []CredentialSource{CredentialSourceUser, CredentialSourceOperator}},
		{strategy: UserOnly, want: []CredentialSource{CredentialSourceUser}},
	}
	for _, test := range tests {
		t.Run(string(test.strategy), func(t *testing.T) {
			require.Equal(t, test.want, test.strategy.Sources())
		})
	}

	withoutOperator := UnavailableFailure("acme", ErrKeyNotFound)
	withOperator := UnavailableFailure("acme", errors.New("operator material exists"))
	require.Equal(t, withoutOperator.Kind(), withOperator.Kind())
	require.Equal(t, withoutOperator.SafeMessage(), withOperator.SafeMessage())
	require.NotContains(t, UserOnly.Sources(), CredentialSourceOperator)

	for _, terminal := range []string{"permission", "validation", "timeout", "canceled", "internal"} {
		t.Run(terminal, func(t *testing.T) {
			providerFailure := failureForStrategyTest(terminal)
			require.False(t, CanAdvance(providerFailure))
		})
	}
	for _, eligible := range []failure.Kind{
		failure.Authentication, failure.Permission, failure.Quota,
		failure.Billing, failure.RateLimit,
	} {
		require.True(t, CanAdvance(failure.New(
			eligible,
			"eligible",
			false,
			failure.ProviderDetails{StateScope: failure.ScopeCredential},
			nil,
		)))
	}
}

func failureForStrategyTest(kind string) *failure.Failure {
	return failure.New(failure.Kind(kind), "terminal", false, failure.ProviderDetails{}, nil)
}
