package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImportPositionAndPureReceiptMatchNativeOwners(t *testing.T) {
	for _, kind := range []string{StorageTypeBadger, StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			store, transfer := transferTestStore(t, kind)
			claim := []byte("pure-native-position")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			inspector := transfer.(ImportPositionInspector)
			require.NoError(t, inspector.CheckImportPosition(t.Context(), claim, ImportReplayPosition{}))
			mutations := []CompareAndSwapMutation{{Key: "catalog:presence", NewValue: []byte{}}}
			evidence := strings.Repeat("a", 64)
			expected, err := ImportReconciliationSHA256(claim, 1, "", evidence, mutations)
			require.NoError(t, err)
			receipt, err := transfer.(ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", evidence, mutations)
			require.NoError(t, err)
			require.Equal(t, expected, receipt)
			position := ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			require.NoError(t, inspector.CheckImportPosition(t.Context(), claim, position))
			require.Error(t, inspector.CheckImportPosition(t.Context(), claim, ImportReplayPosition{}))
			mutations[0].NewValue = nil
			absent, err := ImportReconciliationSHA256(claim, 1, "", evidence, mutations)
			require.NoError(t, err)
			require.NotEqual(t, expected, absent)
			require.NoError(t, store.Delete(t.Context(), reconciliationKey(reconciliationDigest(claim), 1)))
			require.Error(t, inspector.CheckImportPosition(t.Context(), claim, position))
		})
	}
}
