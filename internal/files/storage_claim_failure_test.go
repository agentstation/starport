package files

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

var errLostByteAck = errors.New("lost byte claim acknowledgment")

type lostByteAck struct {
	storage.KVStore
	field []byte
}

func (s *lostByteAck) CompareAndSwapBatch(ctx context.Context, writes []storage.CompareAndSwapMutation) error {
	err := s.KVStore.CompareAndSwapBatch(ctx, writes)
	if err != nil {
		return err
	}
	for _, write := range writes {
		if len(s.field) > 0 && strings.HasPrefix(write.Key, limits.StoredBytesPrefix) && bytes.Contains(write.NewValue, s.field) {
			s.field = nil
			return errLostByteAck
		}
	}
	return nil
}

func TestFileClaimsRecoverLostSuccessfulAcknowledgments(t *testing.T) {
	for _, stage := range []string{"attached", "measured", "released"} {
		t.Run(stage, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, backend storage.KVStore) {
				store := &lostByteAck{KVStore: backend}
				records, err := OpenRepository(store)
				require.NoError(t, err)
				meter, err := limits.NewStorageMeter(store)
				require.NoError(t, err)
				blobs, err := blob.NewFilesystem(t.TempDir())
				require.NoError(t, err)
				now := time.Now().UTC()
				service, err := NewService(records, blobs, WithMeter(meter), WithClock(func() time.Time { return now }))
				require.NoError(t, err)
				upload := func(name string) (File, error) {
					return service.Upload(t.Context(), UploadRequest{Account: "a", Filename: name, Purpose: PurposeBatchOutput, Size: 8, StoredBytesBound: 16}, strings.NewReader("12345678"))
				}
				retained, err := upload("retained")
				require.NoError(t, err)
				if stage != "released" {
					store.field = []byte(`"` + stage + `":true`)
				}
				removed, err := upload("removed")
				if stage == "released" {
					require.NoError(t, err)
					store.field = []byte(`"released":true`)
					require.ErrorIs(t, service.Delete(t.Context(), "a", removed.ID), errLostByteAck)
					pending, err := records.Get(t.Context(), "a", removed.ID)
					require.NoError(t, err)
					require.Equal(t, FileStateDeleting, pending.State)
				} else {
					require.ErrorIs(t, err, errLostByteAck)
				}
				now = now.Add(2 * DefaultPendingGrace)
				_, err = service.Sweep(t.Context())
				require.NoError(t, err)
				require.Equal(t, int64(8), storedTotal(t, meter, "a"))
				_, err = service.Get(t.Context(), "a", retained.ID)
				require.NoError(t, err)
				all, err := records.List(t.Context(), "a", 0)
				require.NoError(t, err)
				require.Len(t, all, 1)
			})
		})
	}
}

func TestFilePublicationCannotReviveDeletingMetadata(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := OpenRepository(store)
		require.NoError(t, err)
		pending := sampleFile("a", "one")
		pending.State = FileStatePending
		require.NoError(t, records.Create(t.Context(), pending))
		deleting := pending
		deleting.State = FileStateDeleting
		require.NoError(t, records.Replace(t.Context(), deleting))
		ready := pending
		ready.State = FileStateReady
		require.ErrorIs(t, records.Replace(t.Context(), ready), storage.ErrConflict)
		actual, err := records.Get(t.Context(), "a", "one")
		require.NoError(t, err)
		require.Equal(t, FileStateDeleting, actual.State)
	})
}
