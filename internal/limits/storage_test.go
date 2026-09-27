package limits

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestConcurrentReservationsCannotBothPassABoundThatAdmitsOne(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		meter, err := NewStorageMeter(store)
		require.NoError(t, err)
		ctx := t.Context()
		require.NoError(t, meter.InitializeEmpty(ctx, "a"))
		var wg sync.WaitGroup
		results := make([]error, 8)
		start := make(chan struct{})
		for i := range results {
			wg.Go(func() { <-start; results[i] = meter.Reserve(ctx, "a", fmt.Sprint(i), 600, 1000) })
		}
		close(start)
		wg.Wait()
		admitted := 0
		for _, err := range results {
			if err == nil {
				admitted++
			} else {
				require.ErrorIs(t, err, ErrStorageFull)
			}
		}
		require.Equal(t, 1, admitted)
		total, err := meter.Total(ctx, "a")
		require.NoError(t, err)
		require.Equal(t, int64(600), total)
	})
}

func TestStoredByteClaimsPreserveIdentityAndCapacity(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := t.Context()
		meter, err := NewStorageMeter(store)
		require.NoError(t, err)
		for _, account := range []string{"a", "b"} {
			require.NoError(t, meter.InitializeEmpty(ctx, account))
		}
		require.NoError(t, meter.Reserve(ctx, "a", "one", 400, 1000))
		require.NoError(t, meter.Reserve(ctx, "a", "one", 400, 1000))
		require.ErrorIs(t, meter.Reserve(ctx, "a", "one", 401, 1000), ErrStorageClaimConflict)
		require.NoError(t, meter.Reserve(ctx, "a", "two", 500, 1000))
		require.NoError(t, meter.Reserve(ctx, "b", "one", 5000, 0))
		require.ErrorIs(t, meter.Reserve(ctx, "a", "three", 200, 1000), ErrStorageFull)
		claim, err := meter.Attachment(ctx, "a", "one")
		require.NoError(t, err)
		require.NoError(t, store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{claim}))
		require.ErrorIs(t, meter.Abort(ctx, "a", "one"), ErrStorageClaimConflict)
		require.NoError(t, meter.Resize(ctx, "a", "one", 300))
		require.NoError(t, meter.Resize(ctx, "a", "one", 300))
		require.ErrorIs(t, meter.Resize(ctx, "a", "one", 200), ErrStorageClaimConflict)
		require.NoError(t, meter.Release(ctx, "a", "one"))
		reopened, err := NewStorageMeter(store)
		require.NoError(t, err)
		require.NoError(t, reopened.Release(ctx, "a", "one"))
		require.ErrorIs(t, reopened.Reserve(ctx, "a", "one", 400, 1000), ErrStorageClaimReleased)
		total, err := reopened.Total(ctx, "a")
		require.NoError(t, err)
		require.Equal(t, int64(500), total)
		total, err = reopened.Total(ctx, "b")
		require.NoError(t, err)
		require.Equal(t, int64(5000), total)
		require.NoError(t, reopened.Reserve(ctx, "a", "three", 400, 1000))
	})
}

func TestStoredByteHistoryCannotResetImplicitly(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := t.Context()
		m, err := NewStorageMeter(store)
		require.NoError(t, err)
		require.ErrorIs(t, m.Reserve(ctx, "a", "one", 100, 1000), storage.ErrNotFound)
		require.NoError(t, m.InitializeEmpty(ctx, "a"))
		require.NoError(t, m.Reserve(ctx, "a", "one", 100, 1000))
		require.NoError(t, store.Delete(ctx, byteTotalKey("a")))
		require.ErrorIs(t, m.InitializeEmpty(ctx, "a"), ErrStorageHistoryUnknown)
		require.ErrorIs(t, m.Release(ctx, "a", "one"), storage.ErrNotFound)
		require.NoError(t, store.Set(ctx, byteTotalKey("a"), []byte(`{"version":2,"bytes":-1}`)))
		require.ErrorIs(t, m.Reserve(ctx, "a", "two", 100, 1000), ErrStorageHistoryUnknown)
		require.NoError(t, store.Set(ctx, "limits:v1:stored_bytes:b", []byte("100")))
		require.ErrorIs(t, m.InitializeEmpty(ctx, "b"), ErrStorageHistoryUnknown)
	})
}

func TestTheMeterNamesItsHolder(t *testing.T) {
	meter, err := NewStorageMeter(storage.NewMockStore())
	require.NoError(t, err)
	ctx := context.Background()
	require.ErrorIs(t, meter.Reserve(ctx, "", "one", 100, 1000), ErrInvalidHolder)
	require.ErrorIs(t, meter.Release(ctx, "", "one"), ErrInvalidHolder)
	_, err = meter.Total(ctx, "")
	require.ErrorIs(t, err, ErrInvalidHolder)
	_, err = NewStorageMeter(nil)
	require.ErrorIs(t, err, ErrCounterRequired)
}

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
	require.NotSame(t, limits.StoredBytes, clone.StoredBytes)
	require.Equal(t, bound, *clone.StoredBytes)

	zero := int64(0)
	require.ErrorIs(t, (&Limits{StoredBytes: &zero}).Validate(), ErrInvalidStoredBytes)
	negative := int64(-1)
	require.ErrorIs(t, (&Limits{StoredBytes: &negative}).Validate(), ErrInvalidStoredBytes)
}

func TestUnattachedByteRecoveryFencesDelayedPublication(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := t.Context()
		m, err := NewStorageMeter(store)
		require.NoError(t, err)
		require.NoError(t, m.InitializeEmpty(ctx, "a"))
		for _, id := range []string{"old", "attached", "new"} {
			require.NoError(t, m.Reserve(ctx, "a", id, 100, 1000))
		}
		attach, err := m.Attachment(ctx, "a", "attached")
		require.NoError(t, err)
		require.NoError(t, store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{attach}))
		for _, id := range []string{"old", "attached"} {
			c, old, err := m.readClaim(ctx, "a", id)
			require.NoError(t, err)
			c.CreatedAt = time.Now().Add(-2 * StoredBytesPendingGrace)
			data, err := json.Marshal(c)
			require.NoError(t, err)
			require.NoError(t, store.CompareAndSwap(ctx, byteClaimKey("a", id), old, data))
		}
		delayed, err := m.Attachment(ctx, "a", "old")
		require.NoError(t, err)
		require.NoError(t, m.RecoverPending(ctx))
		require.ErrorIs(t, store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{delayed}), storage.ErrConflict)
		require.ErrorIs(t, m.Reserve(ctx, "a", "old", 100, 1000), ErrStorageClaimReleased)
		require.NoError(t, m.RecoverPending(ctx))
		total, err := m.Total(ctx, "a")
		require.NoError(t, err)
		require.Equal(t, int64(200), total)
	})
}

type byteClaimLostAck struct {
	storage.KVStore
	fail bool
}

func (s *byteClaimLostAck) CompareAndSwapBatch(ctx context.Context, writes []storage.CompareAndSwapMutation) error {
	err := s.KVStore.CompareAndSwapBatch(ctx, writes)
	if err == nil && s.fail {
		s.fail = false
		return context.DeadlineExceeded
	}
	return err
}

func TestByteReservationLostAckAndBackendFailure(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backend storage.KVStore) {
		store := &byteClaimLostAck{KVStore: backend}
		m, err := NewStorageMeter(store)
		require.NoError(t, err)
		ctx := t.Context()
		require.NoError(t, m.InitializeEmpty(ctx, "a"))
		store.fail = true
		require.ErrorIs(t, m.Reserve(ctx, "a", "one", 100, 1000), context.DeadlineExceeded)
		require.NoError(t, m.Reserve(ctx, "a", "one", 100, 1000))
		total, err := m.Total(ctx, "a")
		require.NoError(t, err)
		require.Equal(t, int64(100), total)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		require.Error(t, m.Reserve(canceled, "a", "two", 100, 1000))
		total, err = m.Total(ctx, "a")
		require.NoError(t, err)
		require.Equal(t, int64(100), total)
	})
}

func TestByteClaimRecoveryReachesLaterPages(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := t.Context()
		m, err := NewStorageMeter(store)
		require.NoError(t, err)
		require.NoError(t, m.InitializeEmpty(ctx, "a"))
		for i := range 270 {
			id := fmt.Sprint(i)
			require.NoError(t, m.Reserve(ctx, "a", id, 1, 1000))
			c, old, err := m.readClaim(ctx, "a", id)
			require.NoError(t, err)
			c.CreatedAt = time.Now().Add(-2 * StoredBytesPendingGrace)
			data, err := json.Marshal(c)
			require.NoError(t, err)
			require.NoError(t, store.CompareAndSwap(ctx, byteClaimKey("a", id), old, data))
		}
		require.NoError(t, m.RecoverPending(ctx))
		total, err := m.Total(ctx, "a")
		require.NoError(t, err)
		require.Zero(t, total)
	})
}
