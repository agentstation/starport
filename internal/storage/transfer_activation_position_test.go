package storage

import (
	"context"
	"encoding/json/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type positionImportActivator interface {
	ActivateImportAt(context.Context, []byte, ImportReplayPosition, string) error
}

func TestImportActivationRejectsChangedReplayControls(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			for _, mode := range []string{"missing history", "corrupt cursor", "expiring cursor"} {
				t.Run(mode, func(t *testing.T) {
					store, transfer := transferTestStore(t, kind)
					claim := []byte("changed-position-activation")
					require.NoError(t, transfer.Claim(t.Context(), claim))
					digest, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: "account", NewValue: []byte("active")}})
					require.NoError(t, err)
					switch mode {
					case "missing history":
						require.NoError(t, store.Delete(t.Context(), reconciliationKey(reconciliationDigest(claim), 1)))
					case "corrupt cursor":
						require.NoError(t, store.Set(t.Context(), transferReconciliationCurrent, []byte("changed cursor")))
					case "expiring cursor":
						cursor, err := store.Get(t.Context(), transferReconciliationCurrent)
						require.NoError(t, err)
						require.NoError(t, store.SetWithTTL(t.Context(), transferReconciliationCurrent, cursor, time.Hour))
					}
					err = transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, ImportReplayPosition{Sequence: 1, ReceiptSHA256: digest}, strings.Repeat("d", 64))
					require.Error(t, err)
					require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
					_, err = store.Get(t.Context(), transferActivationCurrent)
					require.ErrorIs(t, err, ErrNotFound)
				})
			}
		})
	}
}

func TestImportActivationCannotCrossConcurrentReplay(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			claim := []byte("concurrent-position-activation")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			replay := transfer.(ImportReconciler)
			first, err := replay.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: "account", NewValue: []byte("active")}})
			require.NoError(t, err)
			start := make(chan struct{})
			var activationErr, replayErr error
			var workers sync.WaitGroup
			workers.Go(func() {
				<-start
				activationErr = transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, ImportReplayPosition{Sequence: 1, ReceiptSHA256: first}, strings.Repeat("d", 64))
			})
			workers.Go(func() {
				<-start
				_, replayErr = replay.ReconcileImport(t.Context(), claim, 2, first, strings.Repeat("b", 64), []CompareAndSwapMutation{{Key: "account", ExpectedValue: []byte("active"), NewValue: []byte("withdrawn")}})
			})
			close(start)
			workers.Wait()
			require.True(t, (activationErr == nil) != (replayErr == nil), "only one native transition can commit")
			value, err := store.Get(t.Context(), "account")
			require.NoError(t, err)
			if activationErr == nil {
				require.Equal(t, []byte("active"), value)
				require.NoError(t, CheckImportBarrier(t.Context(), store))
			} else {
				require.Equal(t, []byte("withdrawn"), value)
				require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			}
		})
	}
}

func TestImportActivationBindsFinalReplayPosition(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			activate, ok := transfer.(positionImportActivator)
			require.True(t, ok, "native activation must bind the exact final replay cursor")
			claim := []byte("position-bound-activation")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			first, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: "account", NewValue: []byte("active")}})
			require.NoError(t, err)
			position := ImportReplayPosition{Sequence: 1, ReceiptSHA256: first}
			decision := strings.Repeat("d", 64)
			for _, wrong := range []ImportReplayPosition{{}, {Sequence: 1, ReceiptSHA256: strings.Repeat("b", 64)}, {Sequence: 2, ReceiptSHA256: first}, {Sequence: -1}} {
				require.Error(t, activate.ActivateImportAt(t.Context(), claim, wrong, decision))
				require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			}
			second, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 2, first, strings.Repeat("b", 64), []CompareAndSwapMutation{{Key: "account", ExpectedValue: []byte("active"), NewValue: []byte("withdrawn")}})
			require.NoError(t, err)
			require.Error(t, activate.ActivateImportAt(t.Context(), claim, position, decision))
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			position = ImportReplayPosition{Sequence: 2, ReceiptSHA256: second}
			require.NoError(t, activate.ActivateImportAt(t.Context(), claim, position, decision))
			require.NoError(t, CheckImportBarrier(t.Context(), store))
			require.NoError(t, store.Set(t.Context(), "account", []byte("later")))
			require.NoError(t, activate.ActivateImportAt(t.Context(), claim, position, decision))
			require.Error(t, activate.ActivateImportAt(t.Context(), claim, ImportReplayPosition{Sequence: 1, ReceiptSHA256: first}, decision))
			value, err := store.Get(t.Context(), "account")
			require.NoError(t, err)
			require.Equal(t, []byte("later"), value)
		})
	}
}

func TestImportActivationBindsZeroAndUnchangedControlHistory(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			activate, ok := transfer.(positionImportActivator)
			require.True(t, ok)
			claim := []byte("zero-position-activation")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, activate.ActivateImportAt(t.Context(), claim, ImportReplayPosition{}, strings.Repeat("a", 64)))
			require.NoError(t, activate.ActivateImportAt(t.Context(), claim, ImportReplayPosition{}, strings.Repeat("a", 64)))
			require.NoError(t, CheckImportBarrier(t.Context(), store))
		})
	}
}

// This optional interface captures the restart contract before native support exists.
type positionedActivationInspector interface {
	CheckActivatedImportAt(context.Context, []byte, ImportReplayPosition, string) error
}

func TestImportActivationRetainsFinalPositionForPassiveRestart(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			inspect, ok := transfer.(positionedActivationInspector)
			require.True(t, ok, "restart requires passive native position-bound activation evidence")
			claim, decision := []byte("retained-position"), strings.Repeat("a", 64)
			require.NoError(t, transfer.Claim(t.Context(), claim))
			position := ImportReplayPosition{}
			require.Error(t, inspect.CheckActivatedImportAt(t.Context(), claim, position, decision))
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
			digest, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("b", 64), []CompareAndSwapMutation{{Key: "account", NewValue: []byte("active")}})
			require.NoError(t, err)
			position = ImportReplayPosition{Sequence: 1, ReceiptSHA256: digest}
			require.NoError(t, transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, position, decision))
			encoded, err := store.Get(t.Context(), transferActivationCurrent)
			require.NoError(t, err)
			var retained struct {
				Version  int                  `json:"version"`
				Position ImportReplayPosition `json:"position"`
			}
			require.NoError(t, json.Unmarshal(encoded, &retained))
			require.Equal(t, 2, retained.Version)
			require.Equal(t, position, retained.Position)
			require.NoError(t, store.Set(t.Context(), "account", []byte("withdrawn")))
			require.NoError(t, inspect.CheckActivatedImportAt(t.Context(), claim, position, decision))
			require.Error(t, inspect.CheckActivatedImportAt(t.Context(), claim, ImportReplayPosition{}, decision))
			require.Error(t, inspect.CheckActivatedImportAt(t.Context(), claim, position, strings.Repeat("c", 64)))
			value, err := store.Get(t.Context(), "account")
			require.NoError(t, err)
			require.Equal(t, []byte("withdrawn"), value)
			require.NoError(t, store.SetWithTTL(t.Context(), transferActivationCurrent, encoded, time.Hour))
			require.Error(t, inspect.CheckActivatedImportAt(t.Context(), claim, position, decision))
		})
	}
}

func TestImportActivationUnpositionedReceiptCannotProveStrictRestart(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			_, transfer := transferTestStore(t, kind)
			inspect, ok := transfer.(positionedActivationInspector)
			require.True(t, ok)
			claim, decision := []byte("unpositioned"), strings.Repeat("a", 64)
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, transfer.(ImportActivator).ActivateImport(t.Context(), claim, decision))
			require.Error(t, inspect.CheckActivatedImportAt(t.Context(), claim, ImportReplayPosition{}, decision))
			require.NoError(t, transfer.(ImportActivator).ActivateImport(t.Context(), claim, decision))
		})
	}
}
