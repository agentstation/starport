package files

import (
	"fmt"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestFilePagesReachPublicFilesAndExpiredPreparations(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := OpenRepository(store)
		require.NoError(t, err)
		blobs, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		now := time.Now().UTC()
		service, err := NewService(records, blobs, WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		fixtures := make(map[string][]byte, 1030)
		for i := range 1030 {
			file := sampleFile("a", fmt.Sprintf("checkpoint-%04d", i))
			file.State = FileStatePending
			file.Purpose = PurposeBatchOutput
			file.outputIdentity = file.ID
			file.CreatedAt = now.Add(-2 * time.Hour)
			file.ExpiresAt = now.Add(time.Hour)
			if i < 10 {
				file.State = FileStateReady
				file.outputDigest = outputHash("checkpoint")
			}
			data, err := encodeFile(file)
			require.NoError(t, err)
			fixtures[storageKey(file.Account, file.ID)] = data
		}
		require.NoError(t, store.BatchSet(t.Context(), fixtures))
		public := sampleFile("a", "zz-public")
		public.CreatedAt = now
		public.ExpiresAt = now.Add(24 * time.Hour)
		require.NoError(t, records.Create(t.Context(), public))
		listed, err := service.List(t.Context(), "a", 10)
		require.NoError(t, err)
		require.Equal(t, []File{public}, listed)
		listed, err = service.List(t.Context(), "", 10)
		require.NoError(t, err)
		require.Empty(t, listed)
		listed, err = service.List(t.Context(), "other", 10)
		require.NoError(t, err)
		require.Empty(t, listed)
		now = now.Add(2 * time.Hour)
		swept, err := service.Sweep(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1030, swept.Expired)
		all, err := records.Scan(t.Context(), 0)
		require.NoError(t, err)
		require.Equal(t, []File{public}, all)
	})
}
