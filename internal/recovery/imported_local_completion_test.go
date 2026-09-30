package recovery

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func importedLocalCompletionFixture(t *testing.T) (*Witness, sqlstore.Config, *storage.LocalRecoveryTarget, ImportedLocalCompletionRequest, sqlstore.RelationalReplayPosition, storage.KVStore) {
	t.Helper()
	witness, sqlConfig, guard := closedImportGuardFixture(t, sqlstore.TypeSQLite)
	store, transfer, cfg := kvTransferStores(t, storage.StorageTypeBadger)
	digest, err := cfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	target, err := storage.OpenLocalRecoveryTarget(t.Context(), store, digest)
	require.NoError(t, err)
	claim := []byte("independent-local-final-claim")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	receipt, err := transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), []storage.CompareAndSwapMutation{{Key: "domain", NewValue: []byte("preserved")}})
	require.NoError(t, err)
	request := ImportedLocalCompletionRequest{Closed: guard.Boundary, Snapshot: guard.Snapshot, Import: guard.Import,
		OperationID: guard.Import.OperationID, Evidence: "complete-local-proof", DecisionSHA256: strings.Repeat("d", 64),
		LocalTargetSHA256: digest, KVClaim: claim, KVPosition: storage.ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}}
	require.NoError(t, transfer.(storage.ImportReplayActivator).ActivateImportAt(t.Context(), claim, request.KVPosition, request.DecisionSHA256))
	return witness, sqlConfig, target, request, guard.Position, store
}

func localCompletionContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestImportedLocalCompletionExactNativePositionAndWithdrawal(t *testing.T) {
	witness, _, target, request, position, store := importedLocalCompletionFixture(t)
	ctx := localCompletionContext(t)
	witness.db.SetMaxOpenConns(1)
	completed, err := witness.CompleteImportedLocalAt(ctx, target, request, position)
	require.NoError(t, err)
	require.True(t, completed.Open)
	require.Equal(t, localRecoveryBackendPrefix+request.LocalTargetSHA256, completed.BackendID)
	require.NoError(t, witness.db.CheckImportBarrier(ctx))
	require.NoError(t, witness.CheckImportedLocalCompletionAt(ctx, target, request, position))
	_, err = store.Get(ctx, authorityKey)
	require.ErrorIs(t, err, storage.ErrNotFound, "local completion creates no fleet native authority")
	_, invented := store.(storage.IncarnationProvider)
	require.False(t, invented)
	require.NoError(t, store.Set(ctx, "domain", []byte("later state")))
	again, err := witness.CompleteImportedLocalAt(ctx, target, request, position)
	require.NoError(t, err)
	require.Equal(t, completed, again)
	value, err := store.Get(ctx, "domain")
	require.NoError(t, err)
	require.Equal(t, []byte("later state"), value)
	withdrawn, err := witness.Close(ctx, completed)
	require.NoError(t, err)
	_, err = witness.CompleteImportedLocalAt(ctx, target, request, position)
	require.ErrorIs(t, err, ErrConflict)
	require.ErrorIs(t, witness.CheckImportedLocalCompletionAt(ctx, target, request, position), ErrConflict)
	current, err := witness.Current(ctx, request.Closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, withdrawn, current)
	require.NoError(t, witness.db.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, position, request.DecisionSHA256), "historical receipt remains valid after withdrawal")
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		require.NotContains(t, fmt.Sprintf(verb, request), request.Evidence)
	}
	require.NotContains(t, fmt.Sprintf("%p", &request), request.Evidence)
}

func TestImportedLocalCompletionRefusesChangedOriginalClaims(t *testing.T) {
	witness, _, target, original, sqlPosition, _ := importedLocalCompletionFixture(t)
	ctx := localCompletionContext(t)
	for _, field := range []string{"deployment", "epoch", "closed-backend", "closed-evidence", "open", "evidence", "operation", "decision", "local-target", "kv-claim", "kv-position", "zero-kv", "negative-kv", "sql-position", "zero-sql", "negative-sql", "snapshot"} {
		t.Run(field, func(t *testing.T) {
			request, position := original, sqlPosition
			switch field {
			case "deployment":
				request.Closed.DeploymentID = "another"
			case "epoch":
				request.Closed.Epoch++
			case "closed-backend":
				request.Closed.BackendID += "-changed"
			case "closed-evidence":
				request.Closed.Evidence += "-changed"
			case "open":
				request.Closed.Open = true
			case "evidence":
				request.Evidence = ""
			case "operation":
				request.OperationID += "-changed"
			case "decision":
				request.DecisionSHA256 = strings.Repeat("e", 64)
			case "local-target":
				request.LocalTargetSHA256 = strings.Repeat("e", 64)
			case "kv-claim":
				request.KVClaim = []byte("changed")
			case "kv-position":
				request.KVPosition.Sequence++
			case "zero-kv":
				request.KVPosition = storage.ImportReplayPosition{}
			case "negative-kv":
				request.KVPosition.Sequence = -1
			case "sql-position":
				position.Sequence++
			case "zero-sql":
				position = sqlstore.RelationalReplayPosition{}
			case "negative-sql":
				position.Sequence = -1
			case "snapshot":
				request.Snapshot.SHA256 = strings.Repeat("e", 64)
			}
			_, err := witness.CompleteImportedLocalAt(ctx, target, request, position)
			require.Error(t, err)
			current, err := witness.Current(ctx, original.Closed.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, original.Closed, current)
			require.ErrorIs(t, witness.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
		})
	}
	_, err := witness.CompleteImportedLocalAt(t.Context(), target, original, sqlPosition)
	require.ErrorIs(t, err, ErrConflict)
	_, err = witness.CompleteImportedLocalAt(ctx, nil, original, sqlPosition)
	require.ErrorIs(t, err, ErrConflict)
	_, err = witness.CompleteImportedLocalAt(ctx, target, original, sqlPosition)
	require.NoError(t, err)
	for _, field := range []string{"evidence", "decision", "target"} {
		t.Run("completed-"+field, func(t *testing.T) {
			request := original
			switch field {
			case "evidence":
				request.Evidence = "different-proof"
			case "decision":
				request.DecisionSHA256 = strings.Repeat("e", 64)
			case "target":
				request.LocalTargetSHA256 = strings.Repeat("e", 64)
			}
			require.Error(t, witness.CheckImportedLocalCompletionAt(ctx, target, request, sqlPosition))
			_, err := witness.CompleteImportedLocalAt(ctx, target, request, sqlPosition)
			require.Error(t, err)
		})
	}
}
