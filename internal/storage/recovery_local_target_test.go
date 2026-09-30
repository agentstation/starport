package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func localRecoveryStoreFixture(t *testing.T) (*BadgerStore, Config) {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o700))
	cfg := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: path, SyncWrites: true, MemTableSize: 8 << 20}}
	store, err := OpenBadger(cfg.Badger)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store, cfg
}

func TestLocalRecoveryTargetNativeBindingAndActivation(t *testing.T) {
	store, cfg := localRecoveryStoreFixture(t)
	expected, err := cfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	target, err := OpenLocalRecoveryTarget(t.Context(), store, expected)
	require.NoError(t, err)
	require.Equal(t, expected, target.TargetSHA256())
	require.NoError(t, target.Check(t.Context()))
	_, invented := any(store).(IncarnationProvider)
	require.False(t, invented)
	transfer, err := OpenRecordTransfer(t.Context(), store, "")
	require.NoError(t, err)
	claim := []byte("private-local-import-claim")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	receipt, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: "domain", NewValue: []byte("original")}})
	require.NoError(t, err)
	position := ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
	decision := strings.Repeat("d", 64)
	require.Error(t, target.CheckActivatedImportAt(t.Context(), claim, position, decision))
	require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
	require.NoError(t, transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, position, decision))
	require.NoError(t, target.CheckActivatedImportAt(t.Context(), claim, position, decision))
	require.NoError(t, store.Set(t.Context(), "domain", []byte("later")))
	require.NoError(t, target.CheckActivatedImportAt(t.Context(), claim, position, decision))
	actual, err := store.Get(t.Context(), "domain")
	require.NoError(t, err)
	require.Equal(t, []byte("later"), actual)
	for _, change := range []string{"claim", "position", "decision"} {
		t.Run(change, func(t *testing.T) {
			c, p, d := claim, position, decision
			switch change {
			case "claim":
				c = []byte("changed")
			case "position":
				p.Sequence++
			case "decision":
				d = strings.Repeat("e", 64)
			}
			require.Error(t, target.CheckActivatedImportAt(t.Context(), c, p, d))
		})
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%p"} {
		require.NotContains(t, fmt.Sprintf(verb, *target), cfg.Badger.Path)
	}
}

func TestLocalRecoveryTargetRefusesNativeDirectoryReplacement(t *testing.T) {
	store, cfg := localRecoveryStoreFixture(t)
	expected, err := cfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	target, err := OpenLocalRecoveryTarget(t.Context(), store, expected)
	require.NoError(t, err)
	original := cfg.Badger.Path + "-original"
	require.NoError(t, store.Close())
	require.NoError(t, os.Rename(cfg.Badger.Path, original))
	require.NoError(t, os.Mkdir(cfg.Badger.Path, 0o700))
	replacement, err := OpenBadger(cfg.Badger)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, replacement.Close()) })
	require.Error(t, target.Check(t.Context()))
	_, err = OpenLocalRecoveryTarget(t.Context(), replacement, expected)
	require.ErrorIs(t, err, ErrConflict)
	current, err := cfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	require.NotEqual(t, expected, current)
	_, err = OpenLocalRecoveryTarget(t.Context(), replacement, current)
	require.NoError(t, err)
}

func TestLocalRecoveryTargetRefusesOtherScopeAndInvalidHandles(t *testing.T) {
	store, cfg := localRecoveryStoreFixture(t)
	expected, err := cfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	other, othercfg := localRecoveryStoreFixture(t)
	otherDigest, err := othercfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	_, err = OpenLocalRecoveryTarget(t.Context(), store, otherDigest)
	require.ErrorIs(t, err, ErrConflict)
	_, err = OpenLocalRecoveryTarget(t.Context(), other, expected)
	require.ErrorIs(t, err, ErrConflict)
	_, err = OpenLocalRecoveryTarget(t.Context(), struct{ KVStore }{store}, expected)
	require.ErrorIs(t, err, ErrConflict)
	_, err = OpenLocalRecoveryTarget(t.Context(), nil, expected)
	require.ErrorIs(t, err, ErrConflict)
	_, err = OpenLocalRecoveryTarget(t.Context(), store, "not-a-digest")
	require.ErrorIs(t, err, ErrConflict)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = OpenLocalRecoveryTarget(canceled, store, expected)
	require.ErrorIs(t, err, context.Canceled)
	var absent *LocalRecoveryTarget
	require.ErrorIs(t, absent.Check(t.Context()), ErrConflict)
	require.Empty(t, absent.TargetSHA256())
}

func TestLocalRecoveryTargetRefusesMemoryUnsynchronizedAndReadOnly(t *testing.T) {
	store, cfg := localRecoveryStoreFixture(t)
	expected, err := cfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	require.NoError(t, store.Close())
	readonly, err := OpenBadgerReadOnly(cfg.Badger)
	if runtime.GOOS == "windows" {
		require.ErrorIs(t, err, ErrReadOnlyUnsupported)
	} else {
		require.NoError(t, err)
		_, err = OpenLocalRecoveryTarget(t.Context(), readonly, expected)
		require.ErrorIs(t, err, ErrConflict)
		require.NoError(t, readonly.Close())
	}
	unsynced := cfg.Badger
	unsynced.SyncWrites = false
	opened, err := OpenBadger(unsynced)
	require.NoError(t, err)
	_, err = OpenLocalRecoveryTarget(t.Context(), opened, expected)
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, opened.Close())
	memory, err := OpenBadger(BadgerConfig{InMemory: true, SyncWrites: true, MemTableSize: 8 << 20})
	require.NoError(t, err)
	defer memory.Close()
	_, err = OpenLocalRecoveryTarget(t.Context(), memory, expected)
	require.ErrorIs(t, err, ErrConflict)
}
