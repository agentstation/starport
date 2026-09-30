package storage

import (
	"encoding/json/v2"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestRecoveryStartupNativeKVControls(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			state, err := InspectRecoveryStartup(t.Context(), store)
			require.NoError(t, err)
			require.False(t, state.Activated)
			claim := []byte("startup-operation")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			_, err = InspectRecoveryStartup(t.Context(), store)
			require.ErrorIs(t, err, ErrImportRestricted)
			require.NoError(t, transferActivator(t, transfer).ActivateImport(t.Context(), claim, strings.Repeat("a", 64)))
			state, err = InspectRecoveryStartup(t.Context(), store)
			require.NoError(t, err)
			require.True(t, state.Activated)
			current, err := store.Get(t.Context(), transferActivationCurrent)
			require.NoError(t, err)
			require.NoError(t, store.SetWithTTL(t.Context(), transferActivationCurrent, current, time.Hour))
			_, err = InspectRecoveryStartup(t.Context(), store)
			require.ErrorIs(t, err, ErrImportRestricted)
			require.NoError(t, store.Set(t.Context(), transferActivationCurrent, current))
			require.NoError(t, store.Delete(t.Context(), transferActivationCurrent))
			_, err = InspectRecoveryStartup(t.Context(), store)
			require.ErrorIs(t, err, ErrImportRestricted, "retained native history must not become first boot")
		})
	}
}

func TestRecoveryStartupDefersBadgerMaintenance(t *testing.T) {
	cfg := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 8 << 20}}
	opened, err := OpenForStartup(cfg)
	require.NoError(t, err)
	store := opened.(*BadgerStore)
	require.Nil(t, store.gcTicker)
	require.Nil(t, store.compactTicker)
	require.NoError(t, store.StartMaintenance())
	gc, compact := store.gcTicker, store.compactTicker
	require.NotNil(t, gc)
	require.NotNil(t, compact)
	require.NoError(t, store.StartMaintenance())
	require.Same(t, gc, store.gcTicker)
	require.Same(t, compact, store.compactTicker)
	require.NoError(t, store.Close())
	readonly, err := OpenBadgerReadOnly(cfg.Badger)
	if err != nil {
		require.ErrorIs(t, err, ErrReadOnlyUnsupported)
		return
	}
	require.ErrorIs(t, readonly.StartMaintenance(), ErrReadOnly)
	require.Nil(t, readonly.gcTicker)
	require.NoError(t, readonly.Close())
}

func TestRecoveryStartupImportedBadgerStartsNoMaintenance(t *testing.T) {
	cfg := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 8 << 20}}
	store, err := openBadgerConnection(cfg.Badger, false, false)
	require.NoError(t, err)
	transfer, err := OpenRecordTransfer(t.Context(), store, "")
	require.NoError(t, err)
	require.NoError(t, transfer.Claim(t.Context(), []byte("closed-import")))
	require.ErrorIs(t, store.StartMaintenance(), ErrImportRestricted)
	require.Nil(t, store.gcTicker)
	require.Nil(t, store.compactTicker)
	require.NoError(t, store.Close())
	opened, err := Open(cfg)
	require.ErrorIs(t, err, ErrImportRestricted)
	require.Nil(t, opened)
}

func TestRecoveryStartupPositionedKVReceipts(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			claim := []byte("positioned-startup")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			receipt, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("b", 64), []CompareAndSwapMutation{{Key: "record", NewValue: []byte("retained")}})
			require.NoError(t, err)
			position := ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			require.NoError(t, transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, position, strings.Repeat("a", 64)))
			state, err := InspectRecoveryStartup(t.Context(), store)
			require.NoError(t, err)
			require.True(t, state.Activated)
			current, err := store.Get(t.Context(), transferActivationCurrent)
			require.NoError(t, err)
			var activation transferActivationReceipt
			require.NoError(t, json.Unmarshal(current, &activation))
			activation.Position = &ImportReplayPosition{}
			changed, err := json.Marshal(activation)
			require.NoError(t, err)
			require.NoError(t, store.Set(t.Context(), transferActivationCurrent, changed))
			require.NoError(t, store.Set(t.Context(), transferActivationPrefix+activation.ClaimSHA256, changed))
			_, err = InspectRecoveryStartup(t.Context(), store)
			require.ErrorIs(t, err, ErrImportRestricted, "matching receipt digests must not override actual final cursor")
		})
	}
}
