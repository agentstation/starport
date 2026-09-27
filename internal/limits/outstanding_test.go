package limits

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// TestTheTighterOfTheTwoBoundsRefusesFirst states how an account bound and a
// key bound resolve. Both read the same counter, so the smaller satisfies the
// larger and running both would be arithmetic for nothing.
func TestTheTighterOfTheTwoBoundsRefusesFirst(t *testing.T) {
	t.Parallel()

	accountBound := int64(8)
	keyBound := int64(2)

	rule, bounded := TightestOutstandingJobs(
		&Limits{OutstandingJobs: &accountBound},
		&Limits{OutstandingJobs: &keyBound},
	)
	require.True(t, bounded)
	require.Equal(t, keyBound, rule.Limit)
	require.Equal(t, ScopeKey, rule.Scope, "the refusal names the holder an operator has to change")

	rule, bounded = TightestOutstandingJobs(&Limits{OutstandingJobs: &accountBound}, nil)
	require.True(t, bounded)
	require.Equal(t, accountBound, rule.Limit)
	require.Equal(t, ScopeAccount, rule.Scope)

	_, bounded = TightestOutstandingJobs(nil, nil)
	require.False(t, bounded, "an account that states no bound is not bounded by this package")
}

// TestOutstandingJobsJoinsTheLimitVocabulary keeps the new dimension inside the
// three doors every other limit passes through.
func TestOutstandingJobsJoinsTheLimitVocabulary(t *testing.T) {
	t.Parallel()

	empty := int64(0)
	require.ErrorIs(t, (&Limits{OutstandingJobs: &empty}).Validate(), ErrInvalidOutstandingJobs)

	bound := int64(4)
	stated := &Limits{OutstandingJobs: &bound}
	require.NoError(t, stated.Validate())
	require.False(t, stated.IsZero())

	clone := stated.Clone()
	require.Equal(t, bound, *clone.OutstandingJobs)
	*clone.OutstandingJobs = 9
	require.Equal(t, int64(4), *stated.OutstandingJobs, "the clone shares no pointer")
}
