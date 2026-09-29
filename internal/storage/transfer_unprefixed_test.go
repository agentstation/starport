package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func unprefixedValkeyFixture(t *testing.T) (*ValkeyStore, ValkeyConfig) {
	t.Helper()
	address := os.Getenv("TEST_UNPREFIXED_VALKEY_URL")
	if address == "" {
		t.Skip("UNVERIFIED: TEST_UNPREFIXED_VALKEY_URL requires a dedicated empty database")
	}
	config := ValkeyConfig{URL: address, DeploymentID: "unprefixed-" + rand.Text(), AllowInsecure: true}
	store, err := openUnscopedValkeyForTest(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store.(*ValkeyStore), config
}

func TestUnprefixedSnapshotRequiresExplicitSelection(t *testing.T) {
	raw, config := unprefixedValkeyFixture(t)
	key := "preserved:" + rand.Text()
	value := []byte{0, 1, 255, 42}
	require.NoError(t, raw.SetWithTTL(t.Context(), key, value, time.Hour))
	t.Cleanup(func() { require.NoError(t, raw.Delete(context.Background(), key)) })
	expires := transferTestExpiry(t, raw, key)

	ordinary, err := OpenSnapshotSource(t.Context(), Config{Type: StorageTypeValkey, Valkey: config})
	require.NoError(t, err)
	defer func() { require.NoError(t, ordinary.Close()) }()
	require.NoError(t, ordinary.Enumerate(t.Context(), func(TransferRecord) error {
		t.Fatal("ordinary capture read an unprefixed record")
		return nil
	}))

	source, err := OpenUnprefixedValkeySnapshotSource(t.Context(), config)
	require.NoError(t, err)
	defer func() { require.NoError(t, source.Close()) }()
	_, writes := source.(KVStore)
	require.False(t, writes)
	_, imports := source.(RecordTransfer)
	require.False(t, imports)
	var records []TransferRecord
	require.NoError(t, source.Enumerate(t.Context(), func(record TransferRecord) error {
		records = append(records, record)
		return nil
	}))
	require.Equal(t, []TransferRecord{{Key: key, Value: value, ExpiresAtMillis: expires}}, records)
	require.Equal(t, expires, transferTestExpiry(t, raw, key))
	failure := errors.New("capture stopped")
	require.ErrorIs(t, source.Enumerate(t.Context(), func(TransferRecord) error { return failure }), failure)
	require.ErrorIs(t, source.Enumerate(t.Context(), nil), ErrInvalidMutation)
}

func TestUnprefixedSnapshotRefusesMixedNamespacesAndImportBarrier(t *testing.T) {
	for _, key := range []string{"{starport:v1:another:}kv:account", TransferBarrierKey} {
		t.Run(key, func(t *testing.T) {
			raw, config := unprefixedValkeyFixture(t)
			require.NoError(t, raw.Set(t.Context(), key, []byte("retained")))
			t.Cleanup(func() { require.NoError(t, raw.Delete(context.Background(), key)) })
			source, err := OpenUnprefixedValkeySnapshotSource(t.Context(), config)
			if key == TransferBarrierKey {
				require.ErrorIs(t, err, ErrImportRestricted)
				require.Nil(t, source)
			} else {
				require.NoError(t, err)
				defer func() { require.NoError(t, source.Close()) }()
				require.ErrorContains(t, source.Enumerate(t.Context(), func(TransferRecord) error {
					t.Fatal("mixed namespace reached the snapshot")
					return nil
				}), "dedicated source database")
			}
			value, err := raw.Get(t.Context(), key)
			require.NoError(t, err)
			require.Equal(t, "retained", string(value))
		})
	}
}

func TestUnprefixedSnapshotRefusesInvalidSelectionBeforeDial(t *testing.T) {
	for _, config := range []ValkeyConfig{
		{URL: "redis://127.0.0.1:1", AllowInsecure: true},
		{DeploymentID: "selected", URL: "redis://127.0.0.1:1", ClusterMode: true, AllowInsecure: true},
	} {
		source, err := OpenUnprefixedValkeySnapshotSource(t.Context(), config)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "connection failed")
		require.Nil(t, source)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	source, err := OpenUnprefixedValkeySnapshotSource(ctx, ValkeyConfig{})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, source)
}
