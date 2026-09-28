package jobslots

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRepeatedJobReleaseCannotFreeAnotherClaim(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		meter, err := Open(backing)
		require.NoError(t, err)
		ctx := t.Context()
		require.NoError(t, meter.Reserve(ctx, "account", "claim-a", "video-a", "video", 2))
		require.NoError(t, meter.Reserve(ctx, "account", "claim-b", "batch-b", "batch", 2))
		require.NoError(t, meter.Release(ctx, "account", "claim-a"))
		require.NoError(t, meter.Release(ctx, "account", "claim-a"))
		total, err := meter.Total(ctx, "account")
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		reopened, err := Open(backing)
		require.NoError(t, err)
		require.NoError(t, reopened.Release(ctx, "account", "claim-a"))
		require.ErrorIs(t, reopened.Reserve(ctx, "account", "claim-a", "video-a", "video", 2), ErrClaimReleased)
		require.ErrorIs(t, reopened.Release(ctx, "account", "missing"), ErrClaimNotFound)
		require.ErrorIs(t, reopened.Release(ctx, "other", "claim-b"), ErrClaimNotFound)
		total, err = reopened.Total(ctx, "account")
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
	})
}

func TestClaimRetriesPreserveIdentityAndCapacity(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		meter, err := Open(backing)
		require.NoError(t, err)
		ctx := t.Context()
		require.NoError(t, meter.Reserve(ctx, "account", "claim", "job", "video", 1))
		require.NoError(t, meter.Reserve(ctx, "account", "claim", "job", "video", 1))
		require.ErrorIs(t, meter.Reserve(ctx, "account", "claim", "different", "video", 1), ErrClaimConflict)
		require.ErrorIs(t, meter.Reserve(ctx, "account", "claim", "job", "batch", 1), ErrClaimConflict)
		require.ErrorIs(t, meter.Reserve(ctx, "account", "claim", "job", "video", 2), ErrClaimConflict)
		require.ErrorIs(t, meter.Reserve(ctx, "account", "second", "job2", "batch", 1), limits.ErrTooManyOutstandingJobs)
		total, err := meter.Total(ctx, "account")
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		require.NoError(t, meter.Release(ctx, "account", "claim"))
		require.NoError(t, meter.Reserve(ctx, "account", "second", "job2", "batch", 1))
		require.NoError(t, meter.Reserve(ctx, "other", "claim", "job", "video", 1))
	})
}

type uncertainWrite struct {
	storage.KVStore
	after bool
}

func (s uncertainWrite) CompareAndSwapBatch(ctx context.Context, mutations []storage.CompareAndSwapMutation) error {
	if s.after {
		if err := s.KVStore.CompareAndSwapBatch(ctx, mutations); err != nil {
			return err
		}
	}
	return errors.New("storage acknowledgement unavailable")
}

func TestLostClaimAcknowledgementsRecoverWithoutChangingOtherWork(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
				reliable, err := Open(backing)
				require.NoError(t, err)
				ctx := t.Context()
				require.NoError(t, reliable.Reserve(ctx, "account", "other", "other-job", "batch", 3))
				uncertain, err := Open(uncertainWrite{backing, after})
				require.NoError(t, err)
				require.Error(t, uncertain.Reserve(ctx, "account", "claim", "job", "video", 3))
				require.NoError(t, reliable.Reserve(ctx, "account", "claim", "job", "video", 3))
				total, err := reliable.Total(ctx, "account")
				require.NoError(t, err)
				require.Equal(t, int64(2), total)
				require.Error(t, uncertain.Release(ctx, "account", "claim"))
				require.NoError(t, reliable.Release(ctx, "account", "claim"))
				total, err = reliable.Total(ctx, "account")
				require.NoError(t, err)
				require.Equal(t, int64(1), total)
			})
		})
	}
}

func TestConcurrentClaimsAndReleasesPreserveTheBound(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		meter, err := Open(backing)
		require.NoError(t, err)
		ctx := t.Context()
		results := make([]error, 16)
		var wg sync.WaitGroup
		for i := range results {
			wg.Go(func() { results[i] = meter.Reserve(ctx, "account", fmt.Sprint(i), "job", "video", 1) })
		}
		wg.Wait()
		winner := -1
		for i, err := range results {
			if err == nil {
				require.Equal(t, -1, winner)
				winner = i
			} else {
				require.ErrorIs(t, err, limits.ErrTooManyOutstandingJobs)
			}
		}
		require.NotEqual(t, -1, winner)
		for i := range results {
			wg.Go(func() { results[i] = meter.Release(ctx, "account", fmt.Sprint(winner)) })
		}
		wg.Wait()
		for _, err := range results {
			require.NoError(t, err)
		}
		total, err := meter.Total(ctx, "account")
		require.NoError(t, err)
		require.Zero(t, total)
	})
}

func TestLegacyAndMissingCountsRequireRecovery(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		meter, err := Open(backing)
		require.NoError(t, err)
		ctx := t.Context()
		for _, value := range []string{"0", "3", "-1", `{"version":2,"total":-1}`, "bad"} {
			require.NoError(t, backing.Set(ctx, countKey("legacy"), []byte(value)))
			require.ErrorIs(t, meter.Reserve(ctx, "legacy", "claim", "job", "video", 2), ErrHistoryUnknown)
			held, err := backing.Get(ctx, countKey("legacy"))
			require.NoError(t, err)
			require.Equal(t, value, string(held))
		}
		require.NoError(t, meter.Reserve(ctx, "account", "claim", "job", "video", 0))
		_, err = backing.Increment(ctx, countKey("account"), 1)
		require.Error(t, err, "an old scalar writer must refuse the new ownership format")
		_, err = backing.Decrement(ctx, countKey("account"), 1)
		require.Error(t, err, "an old release must refuse the new ownership format")
		total, err := meter.Total(ctx, "account")
		require.NoError(t, err)
		require.Equal(t, int64(1), total)

		require.NoError(t, backing.Delete(ctx, countKey("account")))
		require.ErrorIs(t, meter.Release(ctx, "account", "claim"), ErrHistoryUnknown)
		require.ErrorIs(t, meter.Reserve(ctx, "account", "claim", "job", "video", 0), ErrHistoryUnknown)
		require.NoError(t, backing.Delete(ctx, historyKey("account")))
		require.ErrorIs(t, meter.Reserve(ctx, "account", "new-claim", "new-job", "video", 0), ErrHistoryUnknown)

	})
}

func TestClaimOwnershipSurvivesBadgerReopen(t *testing.T) {
	config := storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, Compression: "snappy", NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20}
	first, err := storage.OpenBadger(config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	meter, err := Open(first)
	require.NoError(t, err)
	require.NoError(t, meter.Reserve(t.Context(), "account", "done", "job-done", "video", 2))
	require.NoError(t, meter.Reserve(t.Context(), "account", "active", "job-active", "batch", 2))
	require.NoError(t, meter.Release(t.Context(), "account", "done"))
	require.NoError(t, first.Close())
	second, err := storage.OpenBadger(config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	reopened, err := Open(second)
	require.NoError(t, err)
	require.NoError(t, reopened.Release(t.Context(), "account", "done"))
	require.ErrorIs(t, reopened.Reserve(t.Context(), "account", "done", "job-done", "video", 2), ErrClaimReleased)
	total, err := reopened.Total(t.Context(), "account")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	held, err := reopened.Get(t.Context(), "account", "active")
	require.NoError(t, err)
	require.False(t, held.Released)
}
