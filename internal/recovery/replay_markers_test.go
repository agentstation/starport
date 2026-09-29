package recovery

import (
	"testing"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBackupRejectsUnfinishedWindowReplay(t *testing.T) {
	for _, backend := range []string{storage.StorageTypeBadger, storage.StorageTypeValkey} {
		t.Run(backend, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, backend)
			source.KV = transfer
			key := reservation.WindowReplayPrefix + "unfinished"
			require.NoError(t, kv.Set(t.Context(), key, []byte(`{"state":"unfinished"}`)))
			_, err := inspectReferenceFixture(t, source, request, destination)
			require.ErrorIs(t, err, reservation.ErrHistoryUnknown)
			retained, err := kv.Get(t.Context(), key)
			require.NoError(t, err)
			require.Equal(t, []byte(`{"state":"unfinished"}`), retained)
		})
	}
}
