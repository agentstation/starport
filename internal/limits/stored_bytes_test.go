package limits

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// TestStoredBytesJoinsTheLimitVocabulary states that the new bound behaves
// like every other one: it validates, it clones deeply, and it counts toward
// whether a holder carries limits at all.
func TestStoredBytesJoinsTheLimitVocabulary(t *testing.T) {
	t.Parallel()
	bound := int64(1 << 20)
	limits := &Limits{StoredBytes: &bound}

	require.NoError(t, limits.Validate())
	require.False(t, limits.IsZero())

	clone := limits.Clone()
	require.NotSame(t, storedbytes.StoredBytes, clone.StoredBytes)
	require.Equal(t, bound, *clone.StoredBytes)

	zero := int64(0)
	require.ErrorIs(t, (&Limits{StoredBytes: &zero}).Validate(), ErrInvalidStoredBytes)
	negative := int64(-1)
	require.ErrorIs(t, (&Limits{StoredBytes: &negative}).Validate(), ErrInvalidStoredBytes)
}
