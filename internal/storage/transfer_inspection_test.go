package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type closedImportInspector interface {
	InspectImport(context.Context, []byte, ImportReplayPosition, func(TransferRecord) error) error
}

func TestImportInspectionRefusesChangedBoundary(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			for _, mode := range []string{"replay", "activation", "callback", "canceled", "expired-barrier", "missing-history", "malformed-cursor"} {
				t.Run(mode, func(t *testing.T) {
					store, transfer := transferTestStore(t, kind)
					inspector := transfer.(ImportInspector)
					claim := []byte("inspection-boundary")
					require.NoError(t, transfer.Claim(t.Context(), claim))
					require.NoError(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "record", Value: []byte("retained")}))
					position := ImportReplayPosition{}
					if mode == "expired-barrier" {
						require.NoError(t, store.ExpireAt(t.Context(), TransferBarrierKey, time.Now().Add(time.Hour)))
					}
					if mode == "missing-history" || mode == "malformed-cursor" {
						receipt, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("b", 64), []CompareAndSwapMutation{{Key: "other", NewValue: []byte("new")}})
						require.NoError(t, err)
						position = ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
						if mode == "missing-history" {
							require.NoError(t, store.Delete(t.Context(), reconciliationKey(reconciliationDigest(claim), 1)))
						} else {
							require.NoError(t, store.Set(t.Context(), transferReconciliationCurrent, []byte("{}")))
						}
					}
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					calls := 0
					err := inspector.InspectImport(ctx, claim, position, func(TransferRecord) error {
						calls++
						if calls != 1 {
							return nil
						}
						switch mode {
						case "replay":
							_, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("b", 64), []CompareAndSwapMutation{{Key: "other", NewValue: []byte("new")}})
							require.NoError(t, err)
						case "activation":
							require.NoError(t, transfer.(ImportActivator).ActivateImport(t.Context(), claim, strings.Repeat("c", 64)))
						case "callback":
							return errors.New("reader failure")
						case "canceled":
							cancel()
						}
						return nil
					})
					require.Error(t, err)
					if mode == "expired-barrier" || mode == "missing-history" || mode == "malformed-cursor" {
						require.Zero(t, calls)
					}
				})
			}
		})
	}
}

func TestImportInspectionKeepsBarrierAndBindsReplay(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			inspector, ok := transfer.(closedImportInspector)
			require.True(t, ok)
			claim := []byte("closed-inspection")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "account", Value: []byte("active")}))
			collect := func(position ImportReplayPosition) (map[string]string, error) {
				values := map[string]string{}
				err := inspector.InspectImport(t.Context(), claim, position, func(record TransferRecord) error { values[record.Key] = string(record.Value); return nil })
				return values, err
			}
			values, err := collect(ImportReplayPosition{})
			require.NoError(t, err)
			require.Equal(t, "active", values["account"])
			require.NotContains(t, values, TransferBarrierKey)
			require.ErrorIs(t, transfer.Enumerate(t.Context(), func(TransferRecord) error { return nil }), ErrImportRestricted)
			receipt, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: "account", ExpectedValue: []byte("active"), NewValue: []byte("revoked")}})
			require.NoError(t, err)
			_, err = collect(ImportReplayPosition{})
			require.Error(t, err)
			values, err = collect(ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt})
			require.NoError(t, err)
			require.Equal(t, "revoked", values["account"])
			require.NotContains(t, values, transferReconciliationCurrent)
			require.Error(t, inspector.InspectImport(t.Context(), []byte("wrong"), ImportReplayPosition{}, func(TransferRecord) error { return nil }))
			require.ErrorIs(t, CheckImportBarrier(t.Context(), store), ErrImportRestricted)
		})
	}
}
