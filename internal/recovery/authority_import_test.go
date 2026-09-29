package recovery

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type lostImportedAuthority struct {
	storage.IncarnationProvider
}

func (p lostImportedAuthority) BindIncarnation(ctx context.Context, identity string) (storage.IncarnationStore, error) {
	bound, err := p.IncarnationProvider.BindIncarnation(ctx, identity)
	return lostImportedAuthorityReply{bound}, err
}

type lostImportedAuthorityReply struct{ storage.IncarnationStore }

func (s lostImportedAuthorityReply) CompareAndSwap(ctx context.Context, mutations []storage.CompareAndSwapMutation, live ...string) error {
	if err := s.IncarnationStore.CompareAndSwap(ctx, mutations, live...); err != nil {
		return err
	}
	return errors.New("lost native authority reply")
}

func TestImportedAuthorityKeepsSQLClosedUntilActivation(t *testing.T) {
	source, _, backend := freshTestStores(t)
	approved, err := source.InitializeFresh(t.Context(), backend, "test-deployment", FreshRequest{OperationID: "source", Evidence: "fixture-fresh"})
	require.NoError(t, err)
	old, err := source.OpenAuthority(t.Context(), backend, approved.DeploymentID)
	require.NoError(t, err)
	closed, err := source.Close(t.Context(), approved)
	require.NoError(t, err)
	parent := privateKVDirectory(t)
	snapshot, err := source.db.SnapshotRelational(t.Context(), filepath.Join(parent, "source"))
	require.NoError(t, err)
	target, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(parent, "target.db")}})
	require.NoError(t, err)
	defer target.Close()
	require.NoError(t, target.Migrate(t.Context()))
	identity := sqlstore.RelationalImportIdentity{OperationID: "activate", RestrictionID: "closed-fixture"}
	require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "source", "starport.db"), snapshot.Snapshot, privateKVDirectory(t), identity, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+1,gate_open=0,bootstrap_allowed=0,backend_id=''")
		return err
	}))
	witness, err := New(target)
	require.NoError(t, err)
	restored, err := witness.Current(t.Context(), approved.DeploymentID)
	require.NoError(t, err)
	require.Greater(t, restored.Epoch, closed.Epoch)
	request := ImportedAuthorityRequest{Closed: restored, BackendID: approved.BackendID, Evidence: "fixture-reconciled", OperationID: identity.OperationID, Snapshot: snapshot.Snapshot, Import: identity, DecisionSHA256: strings.Repeat("a", 64)}
	invalid := request
	invalid.DecisionSHA256 = "invalid"
	_, err = witness.ApproveImportedAuthority(t.Context(), backend, invalid)
	require.Error(t, err)
	// Failure after a committed native write must roll back SQL approval and its receipt.
	_, err = witness.ApproveImportedAuthority(t.Context(), lostImportedAuthority{backend}, request)
	require.ErrorContains(t, err, "lost native authority reply")
	require.ErrorIs(t, target.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	actual, err := witness.Current(t.Context(), restored.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, restored, actual)
	_, err = witness.OpenAuthority(t.Context(), backend, restored.DeploymentID)
	require.ErrorIs(t, err, ErrClosed)
	require.Error(t, old.Check(t.Context()))

	result, err := witness.ApproveImportedAuthority(t.Context(), backend, request)
	require.NoError(t, err)
	require.True(t, result.Open)
	require.NoError(t, target.CheckImportBarrier(t.Context()))
	current, err := witness.OpenAuthority(t.Context(), backend, restored.DeploymentID)
	require.NoError(t, err)
	require.NoError(t, current.Check(t.Context()))
	again, err := witness.ApproveImportedAuthority(t.Context(), backend, request)
	require.NoError(t, err)
	require.Equal(t, result, again)
	changed := request
	changed.DecisionSHA256 = strings.Repeat("b", 64)
	_, err = witness.ApproveImportedAuthority(t.Context(), backend, changed)
	require.Error(t, err)
	withdrawn, err := witness.Close(t.Context(), result)
	require.NoError(t, err)
	_, err = witness.ApproveImportedAuthority(t.Context(), backend, request)
	require.ErrorIs(t, err, ErrConflict)
	actual, err = witness.Current(t.Context(), restored.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, withdrawn, actual, "activation retry cannot restore later withdrawn permission")
}
