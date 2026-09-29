package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type relationalReconciler interface {
	ReconcileRelationalImport(context.Context, SQLiteSnapshot, RelationalImportIdentity, string, string, func(context.Context, *sql.Conn) error) error
}

func TestRelationalReconciliationRetainsBarrier(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			require.Implements(t, (*relationalReconciler)(nil), target)
			reconcile := any(target).(relationalReconciler)
			identity := RelationalImportIdentity{OperationID: "reconcile", RestrictionID: "closed"}
			path := filepath.Join(parent, "snapshot", "starport.db")
			require.NoError(t, target.ImportRelationalOnce(t.Context(), path, snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			apply := func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+10 WHERE deployment_id='deployment'")
				return err
			}
			readEpoch := func() int64 {
				var epoch int64
				require.NoError(t, target.QueryRowContext(t.Context(), "SELECT epoch FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&epoch))
				return epoch
			}
			before := readEpoch()
			failure := errors.New("reconciliation interrupted")
			digest := strings.Repeat("a", 64)
			err := reconcile.ReconcileRelationalImport(t.Context(), snapshot.Snapshot, identity, "epoch", digest, func(ctx context.Context, conn *sql.Conn) error {
				require.NoError(t, apply(ctx, conn))
				return failure
			})
			require.ErrorIs(t, err, failure)
			require.Equal(t, before, readEpoch())
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			err = reconcile.ReconcileRelationalImport(t.Context(), snapshot.Snapshot, identity, "epoch", digest, func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, target.Bind("DELETE FROM sqlstore_meta WHERE name=?"), relationalImportMarker)
				return err
			})
			require.ErrorIs(t, err, ErrImportRestricted, "reconciliation cannot release the import barrier")
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			second, err := Open(config)
			require.NoError(t, err)
			defer second.Close()
			outcomes := make(chan error, 2)
			var group sync.WaitGroup
			for _, db := range []*DB{target, second} {
				group.Go(func() {
					outcomes <- any(db).(relationalReconciler).ReconcileRelationalImport(t.Context(), snapshot.Snapshot, identity, "epoch", digest, apply)
				})
			}
			group.Wait()
			close(outcomes)
			for err := range outcomes {
				require.NoError(t, err)
			}
			require.Equal(t, before+10, readEpoch(), "concurrent exact retries apply once")
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			noApply := func(context.Context, *sql.Conn) error { return errors.New("must not apply") }
			require.NoError(t, any(second).(relationalReconciler).ReconcileRelationalImport(t.Context(), snapshot.Snapshot, identity, "epoch", digest, noApply))
			require.ErrorIs(t, reconcile.ReconcileRelationalImport(t.Context(), snapshot.Snapshot, identity, "epoch", strings.Repeat("b", 64), noApply), ErrImportRestricted)
			changed := identity
			changed.OperationID = "other"
			require.ErrorIs(t, reconcile.ReconcileRelationalImport(t.Context(), snapshot.Snapshot, changed, "epoch", digest, noApply), ErrImportRestricted)
			require.NoError(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, strings.Repeat("c", 64), func(context.Context, *sql.Conn) error { return nil }))
			require.ErrorIs(t, reconcile.ReconcileRelationalImport(t.Context(), snapshot.Snapshot, identity, "epoch", digest, noApply), ErrImportRestricted)
			require.Equal(t, before+10, readEpoch())
		})
	}
}
