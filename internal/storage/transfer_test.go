package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/require"
)

func transferTestStore(t *testing.T, kind string) (KVStore, RecordTransfer) {
	t.Helper()
	var store KVStore
	var err error
	identity := ""
	if kind == StorageTypeBadger {
		store, err = OpenBadger(BadgerConfig{Path: t.TempDir(), SyncWrites: true, MemTableSize: 8 << 20})
	} else {
		address := os.Getenv("TEST_VALKEY_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
		}
		store, err = OpenValkey(ValkeyConfig{URL: address, DeploymentID: "transfer-" + rand.Text(), AllowInsecure: true})
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		if kind == StorageTypeValkey {
			keys, err := store.ScanWithPrefix(context.Background(), "", 1000)
			require.NoError(t, err)
			if len(keys) > 0 {
				require.NoError(t, store.BatchDelete(context.Background(), keys))
			}
		}
		require.NoError(t, store.Close())
	})
	if shared, ok := store.(IncarnationProvider); ok {
		identity, err = shared.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	transfer, err := OpenRecordTransfer(t.Context(), store, identity)
	require.NoError(t, err)
	return store, transfer
}

func transferTestExpiry(t *testing.T, store KVStore, key string) int64 {
	t.Helper()
	switch s := store.(type) {
	case *BadgerStore:
		var expires int64
		err := s.db.View(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(key))
			if err == nil {
				expires = int64(item.ExpiresAt()) * 1000
			}
			return err
		})
		require.NoError(t, err)
		return expires
	case *ValkeyStore:
		expires, err := s.do(t.Context(), s.client.B().Pexpiretime().Key(s.prefix+key).Build()).AsInt64()
		require.NoError(t, err)
		if expires == -1 {
			return 0
		}
		return expires
	default:
		t.Fatal("unexpected storage backend")
		return 0
	}
}

func TestRecordTransferExpiryAndConditionalImport(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			claim := []byte("operation-1/archive-digest")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			require.ErrorIs(t, transfer.Claim(t.Context(), []byte("different")), ErrConflict)
			expires := time.Now().Add(time.Hour).Unix()*1000 + 321
			for _, record := range []TransferRecord{{Key: "permanent", Value: []byte{}}, {Key: "binary\x00key", Value: []byte{0, 255, 10}, ExpiresAtMillis: expires}, {Key: "expired", Value: []byte("must-not-return"), ExpiresAtMillis: 1}} {
				require.NoError(t, transfer.Import(t.Context(), claim, record))
				require.NoError(t, transfer.Import(t.Context(), claim, record))
				if record.Key == "expired" {
					_, err := store.Get(t.Context(), record.Key)
					require.ErrorIs(t, err, ErrNotFound)
					continue
				}
				value, err := store.Get(t.Context(), record.Key)
				require.NoError(t, err)
				require.Equal(t, record.Value, value)
				resolution := transfer.ExpiryResolution().Milliseconds()
				require.Equal(t, record.ExpiresAtMillis/resolution*resolution, transferTestExpiry(t, store, record.Key))
				changed := record
				changed.Value = []byte("conflicting")
				require.ErrorIs(t, transfer.Import(t.Context(), claim, changed), ErrConflict)
			}
			require.ErrorIs(t, transfer.Import(t.Context(), []byte("different"), TransferRecord{Key: "absent", Value: []byte("unsafe")}), ErrConflict)
			require.ErrorIs(t, transfer.Enumerate(t.Context(), func(TransferRecord) error { return nil }), ErrImportRestricted)
		})
	}
}

func TestRecordTransferRefusesExistingAndConcurrentClaims(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			require.NoError(t, store.Set(t.Context(), "existing", []byte("preserved")))
			require.ErrorIs(t, transfer.Claim(t.Context(), []byte("new")), ErrDatabaseNotEmpty)
			require.NoError(t, CheckImportBarrier(t.Context(), store))
			require.NoError(t, store.Delete(t.Context(), "existing"))
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for _, claim := range []string{"one", "two"} {
				wg.Go(func() { results <- transfer.Claim(t.Context(), []byte(claim)) })
			}
			wg.Wait()
			close(results)
			winners := 0
			for err := range results {
				if err == nil {
					winners++
				} else {
					require.True(t, errors.Is(err, ErrConflict) || errors.Is(err, ErrImportRestricted))
				}
			}
			require.Equal(t, 1, winners)
		})
	}
}

func TestRecordTransferEnumeratesExactRecords(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			require.NoError(t, store.Set(t.Context(), "persistent", []byte{0, 255}))
			require.NoError(t, store.SetWithTTL(t.Context(), "expires", []byte("value"), time.Hour+321*time.Millisecond))
			wantExpiry := transferTestExpiry(t, store, "expires")
			found := map[string]TransferRecord{}
			require.NoError(t, transfer.Enumerate(t.Context(), func(record TransferRecord) error { found[record.Key] = record; return nil }))
			require.Len(t, found, 2)
			require.Equal(t, []byte{0, 255}, found["persistent"].Value)
			require.Equal(t, int64(0), found["persistent"].ExpiresAtMillis)
			require.Equal(t, wantExpiry, found["expires"].ExpiresAtMillis)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			require.ErrorIs(t, transfer.Enumerate(ctx, func(TransferRecord) error { return nil }), context.Canceled)
		})
	}
}

func TestRecordTransferRequiresApprovedIncarnation(t *testing.T) {
	store, transfer := transferTestStore(t, StorageTypeValkey)
	v := transfer.(*valkeyTransfer)
	v.bound.identity = "0000000000000000000000000000000000000000:0000000000000000000000000000000000000000"
	require.ErrorIs(t, v.Enumerate(t.Context(), func(TransferRecord) error { return nil }), ErrIncarnationChanged)
	require.ErrorIs(t, v.Claim(t.Context(), []byte("claim")), ErrIncarnationChanged)
	require.ErrorIs(t, v.Import(t.Context(), []byte("claim"), TransferRecord{Key: "data"}), ErrIncarnationChanged)
	_, err := OpenRecordTransfer(t.Context(), store, "")
	require.ErrorIs(t, err, ErrIncarnationChanged)
}

func TestRecordTransferBarrierSurvivesBadgerReopen(t *testing.T) {
	cfg := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 8 << 20}}
	store, err := OpenBadger(cfg.Badger)
	require.NoError(t, err)
	transfer, err := OpenRecordTransfer(t.Context(), store, "")
	require.NoError(t, err)
	require.NoError(t, transfer.Claim(t.Context(), []byte("interrupted")))
	require.NoError(t, transfer.Import(t.Context(), []byte("interrupted"), TransferRecord{Key: "data", Value: []byte("retained")}))
	require.NoError(t, store.Close())
	_, err = Open(cfg)
	require.ErrorIs(t, err, ErrImportRestricted)
	reopened, err := OpenBadger(cfg.Badger)
	require.NoError(t, err)
	defer reopened.Close()
	value, err := reopened.Get(t.Context(), "data")
	require.NoError(t, err)
	require.Equal(t, []byte("retained"), value)
}

func TestRecordTransferRejectsReplacementBackend(t *testing.T) {
	first := incarnationTestStore(t, "TEST_VALKEY_URL")
	second := incarnationTestStore(t, "TEST_VALKEY_REPLACEMENT_URL")
	identity, err := first.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	replacement, err := second.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, identity, replacement)
	transfer := &valkeyTransfer{bound: &valkeyIncarnationStore{store: second, identity: identity}}
	require.ErrorIs(t, transfer.Enumerate(t.Context(), func(TransferRecord) error { return nil }), ErrIncarnationChanged)
	require.ErrorIs(t, transfer.Claim(t.Context(), []byte("unapproved")), ErrIncarnationChanged)
	require.ErrorIs(t, transfer.Import(t.Context(), []byte("unapproved"), TransferRecord{Key: "unapproved"}), ErrIncarnationChanged)
}

func TestRecordTransferRequiresDurableBadgerTarget(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsynced", true: "memory"}[memory], func(t *testing.T) {
			cfg := BadgerConfig{InMemory: memory, SyncWrites: memory, MemTableSize: 8 << 20}
			if !memory {
				cfg.Path = t.TempDir()
			}
			store, err := OpenBadger(cfg)
			require.NoError(t, err)
			defer store.Close()
			transfer, err := OpenRecordTransfer(t.Context(), store, "")
			require.NoError(t, err)
			require.ErrorContains(t, transfer.Claim(t.Context(), []byte("unsafe")), "persistent Badger with sync_writes")
			require.NoError(t, CheckImportBarrier(t.Context(), store))
		})
	}
}
