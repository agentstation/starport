package recovery

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

type checkedLocalActivation interface {
	CompleteImportedLocalCheckedAt(context.Context, *storage.LocalRecoveryTarget, ImportedLocalCompletionRequest, sqlstore.RelationalReplayPosition, func(context.Context) error) (Record, error)
}
type checkedFleetActivation interface {
	ApproveImportedAuthorityCheckedAt(context.Context, storage.IncarnationProvider, ImportedAuthorityRequest, sqlstore.RelationalReplayPosition, func(context.Context) error) (Record, error)
}

func TestLocalActivationCurrentApprovalFailureKeepsSQLClosed(t *testing.T) {
	witness, _, target, request, position, _ := importedLocalCompletionFixture(t)
	approval, ok := any(witness).(checkedLocalActivation)
	require.True(t, ok, "local native approval must recheck current application permission under the final SQL transaction")
	ctx := localCompletionContext(t)
	witness.db.SetMaxOpenConns(1)
	rejected := errors.New("current application permission withdrawn")
	calls := 0
	_, err := approval.CompleteImportedLocalCheckedAt(ctx, target, request, position, func(context.Context) error { calls++; return rejected })
	require.ErrorIs(t, err, rejected)
	require.Equal(t, 1, calls)
	current, err := witness.Current(ctx, request.Closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, request.Closed, current)
	require.ErrorIs(t, witness.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
	require.Error(t, witness.db.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, position, request.DecisionSHA256))
	approved, err := approval.CompleteImportedLocalCheckedAt(ctx, target, request, position, func(context.Context) error { calls++; return nil })
	require.NoError(t, err)
	require.True(t, approved.Open)
	require.Equal(t, 2, calls)
	// A completed native retry cannot rerun the approval callback or grant new permission.
	_, err = approval.CompleteImportedLocalCheckedAt(ctx, target, request, position, func(context.Context) error { calls++; return rejected })
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	withdrawn, err := witness.Close(ctx, approved)
	require.NoError(t, err)
	_, err = approval.CompleteImportedLocalCheckedAt(ctx, target, request, position, nil)
	require.ErrorIs(t, err, ErrConflict)
	current, err = witness.Current(ctx, request.Closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, withdrawn, current)
	require.NoError(t, witness.db.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, position, request.DecisionSHA256))
}

func TestFleetActivationCurrentApprovalFailureKeepsSQLAndAuthorityClosed(t *testing.T) {
	for _, kind := range []string{sqlstore.TypePostgres, sqlstore.TypeMySQL} {
		t.Run(kind, func(t *testing.T) {
			witness, _, guard := closedImportGuardFixture(t, kind)
			_, kv, backend := freshTestStores(t)
			incarnation, err := backend.ObserveIncarnation(t.Context())
			require.NoError(t, err)
			request := ImportedAuthorityRequest{Closed: guard.Boundary, BackendID: incarnation, Evidence: "current-permission-procedure", OperationID: guard.Import.OperationID, Snapshot: guard.Snapshot, Import: guard.Import, DecisionSHA256: strings.Repeat("d", 64)}
			approval, ok := any(witness).(checkedFleetActivation)
			require.True(t, ok, "fleet native approval must recheck current application permission under the final SQL transaction")
			ctx := localCompletionContext(t)
			witness.db.SetMaxOpenConns(1)
			denied := errors.New("current catalog permission cannot be established")
			calls := 0
			_, err = approval.ApproveImportedAuthorityCheckedAt(ctx, backend, request, guard.Position, func(context.Context) error { calls++; return denied })
			require.ErrorIs(t, err, denied)
			require.Equal(t, 1, calls)
			current, err := witness.Current(ctx, request.Closed.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, request.Closed, current)
			require.ErrorIs(t, witness.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
			_, err = kv.Get(ctx, authorityKey)
			require.ErrorIs(t, err, storage.ErrNotFound)
			approved, err := approval.ApproveImportedAuthorityCheckedAt(ctx, backend, request, guard.Position, func(context.Context) error { calls++; return nil })
			require.NoError(t, err)
			require.True(t, approved.Open)
			require.Equal(t, 2, calls)
			_, err = approval.ApproveImportedAuthorityCheckedAt(ctx, backend, request, guard.Position, func(context.Context) error { calls++; return denied })
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			withdrawn, err := witness.Close(ctx, approved)
			require.NoError(t, err)
			_, err = approval.ApproveImportedAuthorityCheckedAt(ctx, backend, request, guard.Position, nil)
			require.ErrorIs(t, err, ErrConflict)
			current, err = witness.Current(ctx, request.Closed.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, withdrawn, current)
			require.NoError(t, witness.db.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, guard.Position, request.DecisionSHA256))
		})
	}
}
