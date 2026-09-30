package sqlstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/stretchr/testify/require"
)

func recoveryCensusFixture(t *testing.T, db *DB) RelationalRecoveryCensus {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "private")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	result, err := db.SnapshotSQLite(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	view, err := OpenRelationalSnapshot(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), result.Snapshot, parent)
	require.NoError(t, err)
	census, err := view.RecoveryCensus(t.Context())
	require.NoError(t, err)
	require.NoError(t, view.Close())
	return census
}

func TestRelationalRecoveryCensusIncludesEveryOwnerCategory(t *testing.T) {
	db, _ := sqliteSnapshotFixture(t)
	original := recoveryCensusFixture(t, db)
	_, err := db.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES('unrecognized-control','private bytes')")
	require.NoError(t, err)
	domain := recoveryCensusFixture(t, db)
	require.NotEqual(t, original.DomainSHA256, domain.DomainSHA256)
	require.Equal(t, original.DomainRows+1, domain.DomainRows)
	require.Equal(t, original.ControlSHA256, domain.ControlSHA256)
	_, err = db.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES(?,?)", relationalImportMarker, "closed native claim")
	require.NoError(t, err)
	control := recoveryCensusFixture(t, db)
	require.Equal(t, domain.DomainSHA256, control.DomainSHA256)
	require.NotEqual(t, domain.ControlSHA256, control.ControlSHA256)
	require.Equal(t, domain.ControlRows+1, control.ControlRows)
	_, err = db.ExecContext(t.Context(), "INSERT INTO authorization_revision(id,epoch,sequence) VALUES(1,'original-epoch',3)")
	require.NoError(t, err)
	revision := recoveryCensusFixture(t, db)
	require.Equal(t, control.DomainSHA256, revision.DomainSHA256)
	require.NotEqual(t, control.RevisionSHA256, revision.RevisionSHA256)
	require.EqualValues(t, 1, revision.RevisionRows)
	_, err = db.ExecContext(t.Context(), "INSERT INTO catalog_recovery(deployment_id,epoch,gate_open,backend_id,evidence) VALUES('deployment',4,0,'original-native','retained-boundary')")
	require.NoError(t, err)
	witness := recoveryCensusFixture(t, db)
	require.Equal(t, revision.DomainSHA256, witness.DomainSHA256)
	require.NotEqual(t, revision.WitnessSHA256, witness.WitnessSHA256)
	require.Equal(t, revision.WitnessRows+1, witness.WitnessRows)
	_, err = db.ExecContext(t.Context(), "DELETE FROM sqlstore_meta WHERE name='unrecognized-control'")
	require.NoError(t, err)
	missing := recoveryCensusFixture(t, db)
	require.Equal(t, original.DomainSHA256, missing.DomainSHA256)
	require.Equal(t, original.DomainRows, missing.DomainRows)
	require.Equal(t, witness.WitnessSHA256, missing.WitnessSHA256)
	require.Equal(t, witness.RevisionSHA256, missing.RevisionSHA256)
	require.Equal(t, witness.ControlSHA256, missing.ControlSHA256)
}

func TestRelationalRecoveryCensusRefusesCanceledAndClosedView(t *testing.T) {
	db, parent := sqliteSnapshotFixture(t)
	result, err := db.SnapshotSQLite(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	view, err := OpenRelationalSnapshot(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), result.Snapshot, parent)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = view.RecoveryCensus(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, view.Close())
	_, err = view.RecoveryCensus(t.Context())
	require.Error(t, err)
	var absent *RelationalSnapshotView
	_, err = absent.RecoveryCensus(t.Context())
	require.Error(t, err)
}
