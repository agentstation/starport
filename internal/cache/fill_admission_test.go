package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFillAdmissionKeepsWorkWhenWorkersAreBusy holds the admission contract
// on an idle queue: capacity is the only reason to drop a fill. A request
// that fills twice in a row, the exact entry and then its semantic vector,
// lands the second fill while a worker is still booking the first. Admission
// that gave up on a busy counter lock lost about one such fill in a hundred.
func TestFillAdmissionKeepsWorkWhenWorkersAreBusy(t *testing.T) {
	store, err := NewBufferedLocalCache(64, time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	value := []byte("payload")
	const pairs = 5000
	for range pairs {
		require.NoError(t, store.Set(t.Context(), "exact", value, time.Minute))
		_, _, err := store.Get(t.Context(), "index")
		require.NoError(t, err)
		require.NoError(t, store.Set(t.Context(), "vector", value, time.Minute))
		// Drain before the next pair so capacity never explains a drop.
		require.Eventually(t, func() bool {
			return store.FillStatus().RetainedEntries == 0
		}, time.Second, 10*time.Microsecond)
	}
	status := store.FillStatus()
	require.Zero(t, status.DroppedFills, "an idle queue with room must admit every fill")
	require.Zero(t, status.FailedFills)
	require.EqualValues(t, 2*pairs, status.CompletedFills)
}
