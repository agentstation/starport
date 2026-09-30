package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type guardedImportReader interface {
	ReadImportAt(context.Context, []byte, ImportReplayPosition, string, int) (TransferRecord, error)
}

func TestImportRecordReadBindsNativePositionAndByteLimit(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			reader, ok := transfer.(guardedImportReader)
			require.True(t, ok, "catalog preparation needs a bounded native read under the exact closed replay cursor")
			claim := []byte("bounded-import-read")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, transfer.Import(t.Context(), claim, TransferRecord{Key: "catalog:entry", Value: []byte("catalog-value")}))
			result, err := reader.ReadImportAt(t.Context(), claim, ImportReplayPosition{}, "catalog:entry", 13)
			require.NoError(t, err)
			require.Equal(t, TransferRecord{Key: "catalog:entry", Value: []byte("catalog-value")}, result)
			result.Value[0] = 'x'
			_, err = reader.ReadImportAt(t.Context(), claim, ImportReplayPosition{}, "catalog:entry", 1)
			require.ErrorIs(t, err, ErrValueTooLarge)
			_, err = reader.ReadImportAt(t.Context(), claim, ImportReplayPosition{}, "catalog:missing", 10)
			require.ErrorIs(t, err, ErrNotFound)
			next, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []CompareAndSwapMutation{{Key: "catalog:entry", ExpectedValue: []byte("catalog-value"), NewValue: []byte("updated")}})
			require.NoError(t, err)
			_, err = reader.ReadImportAt(t.Context(), claim, ImportReplayPosition{}, "catalog:entry", 13)
			require.Error(t, err)
			position := ImportReplayPosition{Sequence: 1, ReceiptSHA256: next}
			result, err = reader.ReadImportAt(t.Context(), claim, position, "catalog:entry", 13)
			require.NoError(t, err)
			require.Equal(t, []byte("updated"), result.Value)
			for _, key := range []string{TransferBarrierKey, transferReconciliationCurrent, transferActivationCurrent, ""} {
				_, err := reader.ReadImportAt(t.Context(), claim, position, key, 4096)
				require.Error(t, err)
			}
			require.NoError(t, transfer.(ImportReplayActivator).ActivateImportAt(t.Context(), claim, position, strings.Repeat("b", 64)))
			_, err = reader.ReadImportAt(t.Context(), claim, position, "catalog:entry", 13)
			require.Error(t, err)
			require.NoError(t, CheckImportBarrier(t.Context(), store))
		})
	}
}

func TestImportRecordReadRefusesInvalidBoundsAndCanceledContext(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			_, transfer := transferTestStore(t, kind)
			reader, ok := transfer.(guardedImportReader)
			require.True(t, ok)
			claim := []byte("invalid-import-read")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			for _, limit := range []int{-1, 0, TransferMaxValueBytes + 1} {
				_, err := reader.ReadImportAt(t.Context(), claim, ImportReplayPosition{}, "catalog:entry", limit)
				require.Error(t, err)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			_, err := reader.ReadImportAt(canceled, claim, ImportReplayPosition{}, "catalog:entry", 13)
			require.ErrorIs(t, err, context.Canceled)
			_, err = reader.ReadImportAt(nil, claim, ImportReplayPosition{}, "catalog:entry", 13)
			require.Error(t, err)
		})
	}
}
