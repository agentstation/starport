package recovery

import (
	"context"
	"database/sql"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestUnreleasedKVInspectsOriginalClaimAtReplayPosition(t *testing.T) {
	for _, kind := range []string{storage.StorageTypeBadger, storage.StorageTypeValkey} {
		t.Run(kind, func(t *testing.T) {
			_, transfer, _ := kvTransferStores(t, kind)
			check := transfer.(storage.ImportUnreleasedInspector)
			claim := []byte("original-bounded-coordinator-import")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, check.CheckUnreleasedImport(t.Context(), claim))
			require.Error(t, check.CheckUnreleasedImport(t.Context(), []byte("changed-import")))
			receipt, err := transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []storage.CompareAndSwapMutation{{Key: "domain", NewValue: []byte("preserved")}})
			require.NoError(t, err)
			require.NoError(t, check.CheckUnreleasedImport(t.Context(), claim), "original replay progress stays closed")
			position := storage.ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			decision := strings.Repeat("d", 64)
			require.NoError(t, transfer.(storage.ImportReplayActivator).ActivateImportAt(t.Context(), claim, position, decision))
			require.Error(t, check.CheckUnreleasedImport(t.Context(), claim))
			require.NoError(t, transfer.(storage.ImportActivationInspector).CheckActivatedImportAt(t.Context(), claim, position, decision))
		})
	}
}

func TestUnreleasedSQLInspectsOriginalClaimAtReplayPosition(t *testing.T) {
	for _, kind := range []string{sqlstore.TypeSQLite, sqlstore.TypePostgres, sqlstore.TypeMySQL} {
		t.Run(kind, func(t *testing.T) {
			witness, _, guard := closedImportGuardFixture(t, kind)
			require.NoError(t, witness.db.CheckUnreleasedRelationalImport(t.Context(), guard.Snapshot, guard.Import))
			changed := guard.Import
			changed.OperationID += "-changed"
			require.Error(t, witness.db.CheckUnreleasedRelationalImport(t.Context(), guard.Snapshot, changed))
			decision := strings.Repeat("d", 64)
			require.NoError(t, witness.db.ActivateRelationalImportAt(t.Context(), guard.Snapshot, guard.Import, guard.Position, decision, func(context.Context, *sql.Conn) error { return nil }))
			require.Error(t, witness.db.CheckUnreleasedRelationalImport(t.Context(), guard.Snapshot, guard.Import))
			require.NoError(t, witness.db.CheckActivatedRelationalImportAt(t.Context(), guard.Snapshot, guard.Import, guard.Position, decision))
		})
	}
}
