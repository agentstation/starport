package storage

import (
	"context"
	"crypto/rand"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenImportTargetPreservesRestrictedRetry(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			config := Config{Type: kind, Badger: BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 8 << 20}}
			if kind == StorageTypeValkey {
				if os.Getenv("TEST_VALKEY_URL") == "" {
					t.Skip("UNVERIFIED: TEST_VALKEY_URL is required")
				}
				config.Valkey = ValkeyConfig{URL: os.Getenv("TEST_VALKEY_URL"), DeploymentID: "import-target-" + rand.Text(), AllowInsecure: true}
			}
			store, transfer, err := OpenImportTarget(t.Context(), config)
			require.NoError(t, err)
			if badger, ok := store.(*BadgerStore); ok {
				require.Nil(t, badger.gcTicker)
				require.Nil(t, badger.compactTicker)
			}
			claim := []byte("operation")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "retained", Value: []byte("unchanged")}))
			require.NoError(t, store.Close())
			_, err = Open(config)
			require.ErrorIs(t, err, ErrImportRestricted)
			store, transfer, err = OpenImportTarget(t.Context(), config)
			require.NoError(t, err)
			t.Cleanup(func() {
				if kind == StorageTypeValkey {
					keys, err := store.ScanWithPrefix(context.Background(), "", 1000)
					require.NoError(t, err)
					require.NoError(t, store.BatchDelete(context.Background(), keys))
				}
				require.NoError(t, store.Close())
			})
			require.NoError(t, transfer.Claim(t.Context(), claim))
			value, err := store.Get(t.Context(), "retained")
			require.NoError(t, err)
			require.Equal(t, "unchanged", string(value))
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
		})
	}
}

func TestOpenImportTargetRefusesEphemeralStorage(t *testing.T) {
	_, _, err := OpenImportTarget(t.Context(), Config{Type: StorageTypeBadger, Badger: BadgerConfig{InMemory: true, NumVersions: 1, MemTableSize: 8 << 20}})
	require.ErrorContains(t, err, "persistent")
}
