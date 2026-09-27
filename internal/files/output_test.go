package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func outputHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

type lostOutputAck struct {
	blob.Store
	fail bool
}

func (b *lostOutputAck) Put(ctx context.Context, key string, r io.Reader) (blob.Info, error) {
	info, err := b.Store.Put(ctx, key, r)
	if err == nil && b.fail {
		b.fail = false
		return blob.Info{}, context.DeadlineExceeded
	}
	return info, err
}

func TestPreparedOutputRetainsIdentityBytesAndQuota(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := t.Context()
		fault := &outputPublicationLostAck{KVStore: store}
		records, err := OpenRepository(fault)
		require.NoError(t, err)
		meter, err := limits.NewStorageMeter(store)
		require.NoError(t, err)
		disk, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		blobs := &lostOutputAck{Store: disk}
		now := time.Now().UTC()
		service, err := NewService(records, blobs, WithMeter(meter), WithRetention(24*time.Hour), WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		first, err := service.PrepareOutput(ctx, "a", "one", "one.jsonl", 8)
		require.NoError(t, err)
		_, err = service.Get(ctx, "a", first.ID)
		require.ErrorIs(t, err, ErrFileNotFound)
		_, err = service.RecoverOutput(ctx, "a", first.ID)
		require.ErrorIs(t, err, ErrOutputIncomplete)
		now = now.Add(2 * time.Hour)
		swept, err := service.Sweep(ctx)
		require.NoError(t, err)
		require.Zero(t, swept.Total())
		retry, err := service.PrepareOutput(ctx, "a", "one", "one.jsonl", 8)
		require.NoError(t, err)
		require.Equal(t, first, retry)
		_, err = service.PrepareOutput(ctx, "a", "one", "changed.jsonl", 8)
		require.ErrorIs(t, err, ErrOutputConflict)
		_, err = service.CommitOutput(ctx, "other", first.ID, 8, outputHash("12345678"), strings.NewReader("12345678"))
		require.ErrorIs(t, err, ErrFileNotFound)
		_, err = service.CommitOutput(ctx, "a", first.ID, 8, outputHash("12345678"), strings.NewReader("bad"))
		require.ErrorIs(t, err, ErrOutputConflict)
		require.Equal(t, int64(8), storedTotal(t, meter, "a"))
		changed, err := records.Get(ctx, "a", first.ID)
		require.NoError(t, err)
		changed.State = FileStateReady
		changed.Bytes++
		require.ErrorIs(t, records.Replace(ctx, changed), storage.ErrConflict)
		_, err = service.RecoverOutput(ctx, "a", first.ID)
		require.ErrorIs(t, err, ErrOutputIncomplete)
		_, err = service.CommitOutput(ctx, "a", first.ID, 8, outputHash("87654321"), strings.NewReader("87654321"))
		require.ErrorIs(t, err, ErrOutputConflict)
		blobs.fail = true
		_, err = service.CommitOutput(ctx, "a", first.ID, 8, outputHash("12345678"), strings.NewReader("12345678"))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		reopened, err := NewService(records, disk, WithMeter(meter), WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		fault.fail = true
		ready, err := reopened.RecoverOutput(ctx, "a", first.ID)
		require.NoError(t, err)
		require.Equal(t, FileStateReady, ready.State)
		_, err = reopened.CommitOutput(ctx, "a", first.ID, 8, outputHash("12345678"), strings.NewReader("12345678"))
		require.NoError(t, err)
		require.Equal(t, int64(8), storedTotal(t, meter, "a"))
		_, reader, err := reopened.Open(ctx, "a", first.ID)
		require.NoError(t, err)
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, "12345678", string(body))
		second, err := service.PrepareOutput(ctx, "a", "two", "two.jsonl", 8)
		require.NoError(t, err)
		_, err = service.CommitOutput(ctx, "a", second.ID, 1, outputHash("x"), strings.NewReader("x"))
		require.ErrorIs(t, err, limits.ErrStorageFull)
		require.NoError(t, service.Delete(ctx, "a", first.ID))
		_, err = service.CommitOutput(ctx, "a", second.ID, 1, outputHash("x"), strings.NewReader("x"))
		require.NoError(t, err)
		require.Equal(t, int64(1), storedTotal(t, meter, "a"))
		now = now.Add(25 * time.Hour)
		_, err = service.RecoverOutput(ctx, "a", second.ID)
		require.ErrorIs(t, err, ErrOutputExpired)
		_, err = service.PrepareOutput(ctx, "a", "two", "two.jsonl", 8)
		require.ErrorIs(t, err, ErrOutputExpired)
		_, err = service.Sweep(ctx)
		require.NoError(t, err)
		require.Zero(t, storedTotal(t, meter, "a"))
	})
}

func TestOutputReaderRejectsChangedLengthAndDigest(t *testing.T) {
	for _, body := range []string{"", "short", "12345678x", "abcdefgh"} {
		t.Run(body, func(t *testing.T) {
			service, _, _ := newService(t)
			file, err := service.PrepareOutput(t.Context(), "a", "one", "result", 0)
			require.NoError(t, err)
			_, err = service.CommitOutput(t.Context(), "a", file.ID, 8, outputHash("12345678"), strings.NewReader(body))
			require.True(t, errors.Is(err, ErrOutputConflict), "error = %v", err)
			_, err = service.Get(t.Context(), "a", file.ID)
			require.ErrorIs(t, err, ErrFileNotFound)
		})
	}
}

func TestPreparedOutputAcceptsOneConcurrentResult(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := OpenRepository(store)
		require.NoError(t, err)
		meter, err := limits.NewStorageMeter(store)
		require.NoError(t, err)
		blobs, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		service, err := NewService(records, blobs, WithMeter(meter))
		require.NoError(t, err)
		file, err := service.PrepareOutput(t.Context(), "a", "one", "result", 1)
		require.NoError(t, err)
		var group sync.WaitGroup
		results := make([]error, 2)
		for i, body := range []string{"a", "b"} {
			group.Go(func() {
				_, results[i] = service.CommitOutput(t.Context(), "a", file.ID, 1, outputHash(body), strings.NewReader(body))
			})
		}
		group.Wait()
		wins := 0
		for _, err := range results {
			if err == nil {
				wins++
			} else {
				require.ErrorIs(t, err, ErrOutputConflict)
			}
		}
		require.Equal(t, 1, wins)
		require.Equal(t, int64(1), storedTotal(t, meter, "a"))
		ready, err := service.RecoverOutput(t.Context(), "a", file.ID)
		require.NoError(t, err)
		require.Equal(t, FileStateReady, ready.State)
	})
}

type outputPublicationLostAck struct {
	storage.KVStore
	fail bool
}

func (s *outputPublicationLostAck) CompareAndSwap(ctx context.Context, key string, previous, next []byte) error {
	err := s.KVStore.CompareAndSwap(ctx, key, previous, next)
	if err == nil && s.fail && strings.HasPrefix(key, StoragePrefix) && strings.Contains(string(next), `"state":"ready"`) {
		s.fail = false
		return context.DeadlineExceeded
	}
	return err
}
