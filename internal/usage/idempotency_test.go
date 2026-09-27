package usage

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestUsageReplayDoesNotIncreaseTotals(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{})
		require.NoError(t, err)
		record := testRecord("key", "job-stable", time.Now().UTC())
		record.AccountID, record.TeamID = "account", "team"
		for range 3 {
			require.NoError(t, repository.Put(t.Context(), record))
		}
		for _, scope := range []Scope{GatewayScope(), KeyScope("key"), AccountScope("account"), TeamScope("team")} {
			for _, interval := range []string{IntervalDay, IntervalWeek, IntervalMonth} {
				totals, err := repository.Totals(t.Context(), scope, interval, record.Timestamp)
				require.NoError(t, err)
				require.Equal(t, Totals{Requests: 1, Tokens: 150, SpendNanoUSD: 1250000}, totals)
			}
		}
	})
}

var errUsageWriteInterrupted = errors.New("usage write interrupted")

type interruptedUsageStore struct {
	storage.KVStore
	after bool
	once  sync.Once
}

func (s *interruptedUsageStore) Increment(context.Context, string, int64) (int64, error) {
	return 0, errUsageWriteInterrupted
}

func (s *interruptedUsageStore) CompareAndSwapBatch(ctx context.Context, writes []storage.CompareAndSwapMutation) error {
	interrupted := false
	s.once.Do(func() { interrupted = true })
	if interrupted {
		if s.after {
			if err := s.KVStore.CompareAndSwapBatch(ctx, writes); err != nil {
				return err
			}
		}
		return errUsageWriteInterrupted
	}
	return s.KVStore.CompareAndSwapBatch(ctx, writes)
}

func TestUsageAtomicWriteAndLostAcknowledgement(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "before commit"
		if after {
			name = "after commit"
		}
		t.Run(name, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				wrapper := &interruptedUsageStore{KVStore: store, after: after}
				repository, err := Open(wrapper, Options{})
				require.NoError(t, err)
				record := testRecord("key", "job-stable", time.Now().UTC())
				require.ErrorIs(t, repository.Put(t.Context(), record), errUsageWriteInterrupted)
				if !after {
					page, err := repository.List(t.Context(), Query{})
					require.NoError(t, err)
					require.Empty(t, page.Records, "a failed atomic write must not leave a usage record")
				}
				require.NoError(t, repository.Put(t.Context(), record))
				totals, err := repository.Totals(t.Context(), KeyScope("key"), IntervalDay, record.Timestamp)
				require.NoError(t, err)
				require.Equal(t, Totals{Requests: 1, Tokens: 150, SpendNanoUSD: 1250000}, totals)
			})
		})
	}
}

func TestUsageConcurrentReplayAndConflictingContents(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{})
		require.NoError(t, err)
		record := testRecord("key", "job-stable", time.Now().UTC())
		var wg sync.WaitGroup
		failures := make(chan error, 16)
		for range 16 {
			wg.Go(func() { failures <- repository.Put(t.Context(), record) })
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
		conflicting := record
		conflicting.Tokens.Total++
		require.ErrorIs(t, repository.Put(t.Context(), conflicting), ErrRecordConflict)
		totals, err := repository.Totals(t.Context(), KeyScope("key"), IntervalDay, record.Timestamp)
		require.NoError(t, err)
		require.Equal(t, Totals{Requests: 1, Tokens: 150, SpendNanoUSD: 1250000}, totals)
	})
}

func TestUsageExpiredReplayCannotRecreateTotals(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{Retention: time.Hour})
		require.NoError(t, err)
		record := testRecord("key", "old", time.Now().Add(-2*time.Hour))
		require.ErrorIs(t, repository.Put(t.Context(), record), ErrRecordExpired)
		keys, err := store.ScanWithPrefix(t.Context(), StoragePrefix, 0)
		require.NoError(t, err)
		require.Empty(t, keys)
	})
}

func TestUsageCounterFailureLeavesEveryWriteUnchanged(t *testing.T) {
	for _, value := range []string{"not-an-integer", "-1", "9223372036854775807"} {
		t.Run(value, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				repository, err := Open(store, Options{})
				require.NoError(t, err)
				record := testRecord("key", "pending", time.Now().UTC())
				start, _ := window(IntervalMonth, record.Timestamp)
				counter := aggregateKey(GatewayScope(), IntervalMonth, start, counterRequests)
				require.NoError(t, store.Set(t.Context(), counter, []byte(value)))
				require.Error(t, repository.Put(t.Context(), record))
				keys, err := store.ScanWithPrefix(t.Context(), StoragePrefix, 0)
				require.NoError(t, err)
				require.Equal(t, []string{counter}, keys)
				kept, err := store.Get(t.Context(), counter)
				require.NoError(t, err)
				require.Equal(t, []byte(value), kept)
			})
		})
	}
}

func TestUsageConcurrentEventsPreserveAllContributions(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{})
		require.NoError(t, err)
		at := time.Now().UTC()
		const events = 16
		var wg sync.WaitGroup
		failures := make(chan error, events)
		for i := range events {
			wg.Go(func() {
				record := testRecord("key", "event-"+strconv.Itoa(i), at)
				record.AccountID, record.TeamID = "account", "team"
				failures <- repository.Put(t.Context(), record)
			})
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
		for _, scope := range []Scope{GatewayScope(), KeyScope("key"), AccountScope("account"), TeamScope("team")} {
			for _, interval := range []string{IntervalDay, IntervalWeek, IntervalMonth} {
				totals, err := repository.Totals(t.Context(), scope, interval, at)
				require.NoError(t, err)
				require.Equal(t, Totals{Requests: events, Tokens: events * 150, SpendNanoUSD: events * 1250000}, totals)
			}
		}
	})
}

// Millisecond expiration reproduces Valkey's TTL precision without wall waits.
type millisecondUsageStore struct{ storage.KVStore }

func (s millisecondUsageStore) CompareAndSwapBatch(ctx context.Context, writes []storage.CompareAndSwapMutation) error {
	for i := range writes {
		if writes[i].TTL > 0 {
			writes[i].TTL = max(time.Millisecond, writes[i].TTL.Truncate(time.Millisecond))
		}
	}
	return s.KVStore.CompareAndSwapBatch(ctx, writes)
}

func TestUsageReceiptSurvivesExpirationPrecision(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := millisecondUsageStore{storage.NewMockStore()}
		t.Cleanup(func() { _ = store.Close() })
		repository, err := Open(store, Options{Retention: 2 * time.Second})
		require.NoError(t, err)
		time.Sleep(500 * time.Microsecond)
		record := testRecord("key", "expiry", time.Now().UTC())
		require.NoError(t, repository.Put(t.Context(), record))
		time.Sleep(1999250 * time.Microsecond)
		require.NoError(t, repository.Put(t.Context(), record))
		totals, err := repository.Totals(t.Context(), KeyScope("key"), IntervalDay, record.Timestamp)
		require.NoError(t, err)
		require.Equal(t, int64(1), totals.Requests)
		time.Sleep(time.Millisecond)
		require.ErrorIs(t, repository.Put(t.Context(), record), ErrRecordExpired)
	})
}

func TestUsageReplayAfterBadgerReopen(t *testing.T) {
	config := storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, Compression: "snappy", NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20}
	store, err := storage.OpenBadger(config)
	require.NoError(t, err)
	repository, err := Open(store, Options{})
	require.NoError(t, err)
	record := testRecord("key", "persisted", time.Now().UTC())
	require.NoError(t, repository.Put(t.Context(), record))
	require.NoError(t, store.Close())

	reopened, err := storage.OpenBadger(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	repository, err = Open(reopened, Options{})
	require.NoError(t, err)
	require.NoError(t, repository.Put(t.Context(), record))
	totals, err := repository.Totals(t.Context(), KeyScope("key"), IntervalDay, record.Timestamp)
	require.NoError(t, err)
	require.Equal(t, Totals{Requests: 1, Tokens: 150, SpendNanoUSD: 1250000}, totals)
}
