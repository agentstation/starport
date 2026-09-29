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

type relationalActivator interface {
	ActivateRelationalImport(context.Context, SQLiteSnapshot, RelationalImportIdentity, string, func(context.Context, *sql.Conn) error) error
}

func TestRelationalActivationAtomicRetry(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			require.Implements(t, (*relationalActivator)(nil), target)
			activate := any(target).(relationalActivator)
			identity := RelationalImportIdentity{OperationID: "atomic-activation", RestrictionID: "closed-policy"}
			path := filepath.Join(parent, "snapshot", "starport.db")
			require.NoError(t, target.ImportRelationalOnce(t.Context(), path, snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			failure := errors.New("activation interrupted")
			callback := func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=1 WHERE deployment_id='deployment'")
				return err
			}
			decision := strings.Repeat("a", 64)
			err := activate.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, decision, func(ctx context.Context, conn *sql.Conn) error {
				require.NoError(t, callback(ctx, conn))
				return failure
			})
			require.ErrorIs(t, err, failure)
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			var opened int
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT gate_open FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&opened))
			require.Zero(t, opened, "callback changes must roll back with the activation receipt")
			require.NoError(t, activate.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, decision, callback))
			require.NoError(t, target.CheckImportBarrier(t.Context()))
			_, err = target.ExecContext(t.Context(), "UPDATE catalog_recovery SET gate_open=0, epoch=epoch+1 WHERE deployment_id='deployment'")
			require.NoError(t, err)
			second, err := Open(config)
			require.NoError(t, err)
			defer second.Close()
			require.NoError(t, any(second).(relationalActivator).ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, decision, func(context.Context, *sql.Conn) error {
				return errors.New("retry must not repeat the approval callback")
			}))
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT gate_open FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&opened))
			require.Zero(t, opened, "retry must preserve later revocation")
			require.Error(t, activate.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, strings.Repeat("b", 64), callback))
			require.Error(t, target.ImportRelationalOnce(t.Context(), path, snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
		})
	}
}

func TestRelationalActivationConcurrentDecisions(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			require.Implements(t, (*relationalActivator)(nil), target)
			identity := RelationalImportIdentity{OperationID: "concurrent-activation", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			second, err := Open(config)
			require.NoError(t, err)
			defer second.Close()
			outcomes := make(chan error, 2)
			var group sync.WaitGroup
			for i, db := range []*DB{target, second} {
				group.Go(func() {
					outcomes <- any(db).(relationalActivator).ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, strings.Repeat([]string{"a", "b"}[i], 64), func(ctx context.Context, conn *sql.Conn) error {
						_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+1 WHERE deployment_id='deployment'")
						return err
					})
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
			require.NoError(t, target.CheckImportBarrier(t.Context()))
		})
	}
}

func TestRelationalActivationRejectsChangedClaim(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			identity := RelationalImportIdentity{OperationID: "validated-activation", RestrictionID: "closed"}
			callback := func(context.Context, *sql.Conn) error { return nil }
			decision := strings.Repeat("a", 64)
			require.Error(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, decision, callback), "absence of a barrier is not completion")
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			for _, changed := range []RelationalImportIdentity{{OperationID: "other", RestrictionID: identity.RestrictionID}, {OperationID: identity.OperationID, RestrictionID: "other"}, {OperationID: "", RestrictionID: "closed"}} {
				require.Error(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, changed, decision, callback))
			}
			changed := snapshot.Snapshot
			changed.Size++
			require.Error(t, target.ActivateRelationalImport(t.Context(), changed, identity, decision, callback))
			for _, invalid := range []string{"", "digest", strings.Repeat("A", 64), strings.Repeat("z", 64)} {
				require.Error(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, invalid, callback))
			}
			require.Error(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, decision, nil))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			require.ErrorIs(t, target.ActivateRelationalImport(ctx, snapshot.Snapshot, identity, decision, callback), context.Canceled)
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			require.NoError(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, decision, callback))
			var receipt string
			require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT value FROM sqlstore_meta WHERE name=?"), relationalActivationCurrent).Scan(&receipt))
			for _, invalid := range []string{"", "corrupt", strings.Repeat("x", 257)} {
				_, err := target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value=? WHERE name=?"), invalid, relationalActivationCurrent)
				require.NoError(t, err)
				require.Error(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, decision, callback))
			}
			_, err = target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value=? WHERE name=?"), receipt, relationalActivationCurrent)
			require.NoError(t, err)
			claim, err := relationalImportClaim(snapshot.Snapshot, identity)
			require.NoError(t, err)
			_, err = target.ExecContext(t.Context(), target.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), relationalImportMarker, string(claim))
			require.NoError(t, err)
			require.Error(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, identity, decision, callback), "a revived barrier cannot replace completion")
		})
	}
}

func TestRelationalActivationHistoryCannotApproveAnotherImport(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	activated := transferDB(t, Config{Type: TypeSQLite})
	oldID := RelationalImportIdentity{OperationID: "old-activation", RestrictionID: "closed"}
	callback := func(context.Context, *sql.Conn) error { return nil }
	decision := strings.Repeat("a", 64)
	require.NoError(t, activated.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), oldID, restrictImportedFixture))
	require.NoError(t, activated.ActivateRelationalImport(t.Context(), snapshot.Snapshot, oldID, decision, callback))
	backup, err := activated.SnapshotRelational(t.Context(), filepath.Join(parent, "again"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			newID := RelationalImportIdentity{OperationID: "new-activation", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "again", "starport.db"), backup.Snapshot, transferDirectory(t), newID, restrictImportedFixture))
			var current, historical int
			require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT COUNT(*) FROM sqlstore_meta WHERE name=?"), relationalActivationCurrent).Scan(&current))
			require.Zero(t, current)
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlstore_meta WHERE name LIKE 'relational-activation-v1:%'").Scan(&historical))
			require.Equal(t, 1, historical)
			require.Error(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, oldID, decision, callback))
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			require.NoError(t, target.ActivateRelationalImport(t.Context(), backup.Snapshot, newID, decision, callback))
			require.NoError(t, target.CheckImportBarrier(t.Context()))
			require.Error(t, target.ActivateRelationalImport(t.Context(), snapshot.Snapshot, oldID, decision, callback))
		})
	}
}
