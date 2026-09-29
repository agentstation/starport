package recovery

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func importedEpochFixture(t *testing.T) (*Witness, ImportedEpochRequest, sqlstore.Config) {
	t.Helper()
	parent := privateKVDirectory(t)
	source, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(parent, "source.db")}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, source.Close()) })
	require.NoError(t, source.Migrate(t.Context()))
	_, err = source.ExecContext(t.Context(), "INSERT INTO catalog_recovery(deployment_id,epoch,gate_open,backend_id,evidence,bootstrap_allowed) VALUES('deployment',2,0,'','fixture',0)")
	require.NoError(t, err)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	config := sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(parent, "target.db")}}
	target, err := sqlstore.Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, target.Close()) })
	require.NoError(t, target.Migrate(t.Context()))
	identity := sqlstore.RelationalImportIdentity{OperationID: "restore", RestrictionID: "fixture-closed"}
	require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, privateKVDirectory(t), identity, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+1")
		return err
	}))
	w, err := New(target)
	require.NoError(t, err)
	prepared, err := w.Current(t.Context(), "deployment")
	require.NoError(t, err)
	request := ImportedEpochRequest{Prepared: prepared, Snapshot: snapshot.Snapshot, Import: identity, Evidence: EpochEvidence{HighestEpoch: 27, SourceSHA256: strings.Repeat("a", 64), Reference: "incident/retained-authority", Operator: "operator"}}
	return w, request, config
}

func TestImportedEpochRetainsClosedAuthority(t *testing.T) {
	w, request, config := importedEpochFixture(t)
	changed := request
	changed.Prepared.Evidence = "not-current"
	_, err := w.PrepareImportedEpoch(t.Context(), changed)
	require.ErrorIs(t, err, ErrConflict)
	current, err := w.Current(t.Context(), request.Prepared.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, request.Prepared, current)
	next, err := w.PrepareImportedEpoch(t.Context(), request)
	require.NoError(t, err)
	require.EqualValues(t, 28, next.Epoch)
	require.False(t, next.Open)
	require.Empty(t, next.BackendID)
	require.ErrorIs(t, w.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	_, err = w.Approved(t.Context(), next.DeploymentID)
	require.ErrorIs(t, err, ErrClosed)
	// A new SQL connection resumes from the retained receipt without changing the epoch.
	db, err := sqlstore.Open(config)
	require.NoError(t, err)
	defer db.Close()
	restarted, err := New(db)
	require.NoError(t, err)
	again, err := restarted.PrepareImportedEpoch(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, next, again)
	changed = request
	changed.Evidence.HighestEpoch++
	_, err = restarted.PrepareImportedEpoch(t.Context(), changed)
	require.Error(t, err)
	changed = request
	changed.Evidence.Operator = "different-operator"
	_, err = restarted.PrepareImportedEpoch(t.Context(), changed)
	require.Error(t, err)
	later, err := w.Close(t.Context(), next)
	require.NoError(t, err)
	_, err = restarted.PrepareImportedEpoch(t.Context(), request)
	require.ErrorIs(t, err, ErrConflict)
	current, err = w.Current(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, later, current, "retry cannot replace a later closed boundary")
}

func TestImportedEpochConcurrentEvidence(t *testing.T) {
	w, request, config := importedEpochFixture(t)
	db, err := sqlstore.Open(config)
	require.NoError(t, err)
	defer db.Close()
	other, err := New(db)
	require.NoError(t, err)
	outcomes := make(chan error, 2)
	var group sync.WaitGroup
	for i, witness := range []*Witness{w, other} {
		group.Go(func() {
			candidate := request
			candidate.Evidence.HighestEpoch += int64(i)
			_, err := witness.PrepareImportedEpoch(t.Context(), candidate)
			outcomes <- err
		})
	}
	group.Wait()
	close(outcomes)
	winners := 0
	for err := range outcomes {
		if err == nil {
			winners++
		}
	}
	require.Equal(t, 1, winners)
	require.ErrorIs(t, w.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	_, err = w.Approved(t.Context(), request.Prepared.DeploymentID)
	require.ErrorIs(t, err, ErrClosed)
}

func TestImportedEpochRejectsIncompleteEvidence(t *testing.T) {
	_, request, _ := importedEpochFixture(t)
	for name, change := range map[string]func(*ImportedEpochRequest){
		"unknown":      func(r *ImportedEpochRequest) { r.Evidence.HighestEpoch = 0 },
		"older":        func(r *ImportedEpochRequest) { r.Evidence.HighestEpoch = 1 },
		"exhausted":    func(r *ImportedEpochRequest) { r.Evidence.HighestEpoch = math.MaxInt64 },
		"cannot-close": func(r *ImportedEpochRequest) { r.Evidence.HighestEpoch = math.MaxInt64 - 1 },
		"no-digest":    func(r *ImportedEpochRequest) { r.Evidence.SourceSHA256 = "" },
		"bad-digest":   func(r *ImportedEpochRequest) { r.Evidence.SourceSHA256 = strings.Repeat("A", 64) },
		"no-operator":  func(r *ImportedEpochRequest) { r.Evidence.Operator = "" },
		"no-reference": func(r *ImportedEpochRequest) { r.Evidence.Reference = "" },
		"control":      func(r *ImportedEpochRequest) { r.Evidence.Reference = "external\x00record" },
		"open":         func(r *ImportedEpochRequest) { r.Prepared.Open = true },
		"backend":      func(r *ImportedEpochRequest) { r.Prepared.BackendID = "prior-primary" },
		"no-boundary":  func(r *ImportedEpochRequest) { r.Prepared.Evidence = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			change(&candidate)
			_, _, err := candidate.next()
			require.Error(t, err)
		})
	}
	request.Evidence.HighestEpoch = request.Prepared.Epoch - 1
	next, _, err := request.next()
	require.NoError(t, err)
	require.Equal(t, request.Prepared.Epoch, next.Epoch)
	request.Evidence.HighestEpoch = math.MaxInt64 - 2
	next, _, err = request.next()
	require.NoError(t, err)
	require.EqualValues(t, math.MaxInt64-1, next.Epoch, "the final epoch must permit later withdrawal")
}
