package files

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

// stalledDeletes wraps a byte store and holds every delete until its context
// ends while stalled. It stands in for the object store that answers slowly
// enough for a pass to run out of its time bound in the middle of a delete.
type stalledDeletes struct {
	blob.Store
	stalled bool
	held    int
}

func (s *stalledDeletes) Retire(ctx context.Context, key string) error {
	if s.stalled {
		s.held++
		<-ctx.Done()
		return ctx.Err()
	}
	return s.Store.Retire(ctx, key)
}

// TestSweepTimeBoundEndsThePassWithoutAnError holds the pass contract on a
// slow store. The 30 s bound exists so that one pass does not run forever. It
// ends the pass the way the 256-record bound does, with the work so far and no
// error, because a store that retires fewer records in 30 s has not failed. The
// record under way stays queued, so the next pass retires it first.
func TestSweepTimeBoundEndsThePassWithoutAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backing, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		bytes := &stalledDeletes{Store: backing}
		store := storage.NewMockStore()
		records, err := OpenRepository(store)
		require.NoError(t, err)
		now := time.Now().UTC()
		service, err := NewService(records, bytes, WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		ctx := context.Background()
		expireFiles(t, store, now, 3)

		// The store stalls, the bound ends the pass, and nothing is an error.
		bytes.stalled = true
		result, err := service.Sweep(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, result.Total())
		require.Equal(t, 1, bytes.held)

		// The next pass retires every record, the stalled one included.
		bytes.stalled = false
		result, err = service.Sweep(ctx)
		require.NoError(t, err)
		require.Equal(t, 3, result.Expired)
		all, err := records.Scan(ctx, 0)
		require.NoError(t, err)
		require.Empty(t, all)
	})
}

// TestSweepReportsTheCallerContextEnding states the other half. The caller's
// own context ending is not the pass bound, and a sweep that swallowed it would
// hide a stopping gateway behind a clean result.
func TestSweepReportsTheCallerContextEnding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backing, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		bytes := &stalledDeletes{Store: backing, stalled: true}
		store := storage.NewMockStore()
		records, err := OpenRepository(store)
		require.NoError(t, err)
		now := time.Now().UTC()
		service, err := NewService(records, bytes, WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		expireFiles(t, store, now, 1)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		_, err = service.Sweep(ctx)
		require.ErrorIs(t, err, context.Canceled)
	})
}

// expireFiles writes count expired pending files straight into the store, the
// way the page tests do, so a sweep has records to retire.
func expireFiles(t *testing.T, store storage.KVStore, now time.Time, count int) {
	t.Helper()
	fixtures := make(map[string][]byte, count)
	for i := range count {
		file := sampleFile("a", fmt.Sprintf("stalled-%04d", i))
		file.State = FileStatePending
		file.Purpose = PurposeBatchOutput
		file.outputIdentity = file.ID
		file.blobKey = file.ID
		file.CreatedAt = now.Add(-2 * time.Hour)
		file.ExpiresAt = now.Add(-time.Hour)
		data, err := encodeFile(file)
		require.NoError(t, err)
		fixtures[storageKey(file.Account, file.ID)] = data
	}
	require.NoError(t, store.BatchSet(t.Context(), fixtures))
}
