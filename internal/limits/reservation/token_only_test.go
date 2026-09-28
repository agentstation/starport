package reservation

import (
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/stretchr/testify/require"
)

func TestTokenOnlyCannotBypassSpendOrInventFreePricing(t *testing.T) {
	f := openFixture(t, "badger")
	attempt := attemptFixture()
	f.provision(t, &attempt)
	attempt.TokenOnly = true
	attempt.Valuation, attempt.Bound = Valuation{}, nil
	_, err := f.repository.Reserve(t.Context(), attempt)
	require.ErrorIs(t, err, ErrValuation, "token-only mode cannot bypass any spend meter")
	attempt.Rules = []Rule{attempt.Rules[1]}
	require.Equal(t, limits.DimensionTokens, attempt.Rules[0].Meter.Dimension)
	record, err := f.repository.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.Nil(t, record.NanoUSD)
	require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
	require.ErrorIs(t, f.repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "bad", Tokens: 10, Quantities: Quantities{"unexpected": 10}}), ErrValuation)
	require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "measured", Tokens: 10}))
	record, err = f.repository.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Nil(t, record.NanoUSD, "unknown monetary cost remains null after token settlement")
}
