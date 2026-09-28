package files

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

var errRetirementAck = errors.New("retirement acknowledgment lost")

type lostRetirementAck struct {
	blob.Store
	fail bool
}

func (b *lostRetirementAck) Retire(ctx context.Context, key string) error {
	if err := b.Store.Retire(ctx, key); err != nil {
		return err
	}
	if b.fail {
		return errRetirementAck
	}
	return nil
}
func TestLostRetirementAcknowledgmentRetainsCharge(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := OpenRepository(store)
		require.NoError(t, err)
		meter, err := storedbytes.NewStorageMeter(store)
		require.NoError(t, err)
		medium, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		bytes := &lostRetirementAck{Store: medium, fail: true}
		service, err := NewService(records, bytes, WithMeter(meter))
		require.NoError(t, err)
		file, err := service.Upload(t.Context(), UploadRequest{Account: "a", Filename: "x", Purpose: PurposeUserData, Size: 1, StoredBytesBound: 1}, strings.NewReader("x"))
		require.NoError(t, err)
		require.ErrorIs(t, service.Delete(t.Context(), "a", file.ID), errRetirementAck)
		held, err := meter.Total(t.Context(), "a")
		require.NoError(t, err)
		require.Equal(t, int64(1), held)
		retained, err := records.Get(t.Context(), "a", file.ID)
		require.NoError(t, err)
		require.Equal(t, FileStateDeleting, retained.State)
		_, err = medium.StatPublished(t.Context(), file.blobKey)
		require.ErrorIs(t, err, blob.ErrNotFound)
		_, err = medium.Publish(t.Context(), file.blobKey, strings.NewReader("late"))
		require.ErrorIs(t, err, blob.ErrPublicationExists)
		bytes.fail = false
		result, err := service.Sweep(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, result.Resumed)
		held, err = meter.Total(t.Context(), "a")
		require.NoError(t, err)
		require.Zero(t, held)
	})
}
