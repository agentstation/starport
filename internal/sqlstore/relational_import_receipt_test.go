package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelationalImportReceiptRetriesWithoutRepeatingRestrictions(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			calls := 0
			restrict := func(ctx context.Context, conn *sql.Conn) error {
				calls++
				return restrictImportedFixture(ctx, conn)
			}
			identity := RelationalImportIdentity{OperationID: "restore-one", RestrictionID: "closed-epoch-v1"}
			path := filepath.Join(parent, "snapshot", "starport.db")
			require.NoError(t, target.ImportRelationalOnce(t.Context(), path, snapshot.Snapshot, transferDirectory(t), identity, restrict))
			// The same input must resolve a lost acknowledgement without applying policy twice.
			require.NoError(t, target.ImportRelationalOnce(t.Context(), path, snapshot.Snapshot, transferDirectory(t), identity, restrict))
			require.Equal(t, 1, calls)
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			for _, changed := range []RelationalImportIdentity{{OperationID: "other", RestrictionID: identity.RestrictionID}, {OperationID: identity.OperationID, RestrictionID: "different-policy"}} {
				require.Error(t, target.ImportRelationalOnce(t.Context(), path, snapshot.Snapshot, transferDirectory(t), changed, restrict))
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestRelationalImportReceiptRollsBackWithRestriction(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			failure := errors.New("restriction incomplete")
			identity := RelationalImportIdentity{OperationID: "failed", RestrictionID: "closed-epoch-v1"}
			path := filepath.Join(parent, "snapshot", "starport.db")
			err := target.ImportRelationalOnce(t.Context(), path, snapshot.Snapshot, transferDirectory(t), identity, func(ctx context.Context, conn *sql.Conn) error {
				require.NoError(t, restrictImportedFixture(ctx, conn))
				return failure
			})
			require.ErrorIs(t, err, failure)
			var count int
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users").Scan(&count))
			require.Zero(t, count)
			// A different operation can claim the still-empty target after rollback.
			identity.OperationID = "replacement"
			require.NoError(t, target.ImportRelationalOnce(t.Context(), path, snapshot.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
		})
	}
}

func TestRelationalImportReceiptConcurrentRetry(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			second, err := Open(config)
			require.NoError(t, err)
			defer func() { require.NoError(t, second.Close()) }()
			var calls atomic.Int64
			restrict := func(ctx context.Context, conn *sql.Conn) error {
				calls.Add(1)
				return restrictImportedFixture(ctx, conn)
			}
			identity := RelationalImportIdentity{OperationID: "concurrent", RestrictionID: "closed-epoch-v1"}
			var group sync.WaitGroup
			outcomes := make(chan error, 2)
			for _, db := range []*DB{target, second} {
				scratch := transferDirectory(t)
				group.Go(func() {
					outcomes <- db.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, scratch, identity, restrict)
				})
			}
			group.Wait()
			close(outcomes)
			for err := range outcomes {
				require.NoError(t, err)
			}
			require.EqualValues(t, 1, calls.Load())
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
		})
	}
}

func TestRelationalImportReceiptCannotImportRestrictedSource(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	_, err := source.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES('relational-import-v1','incomplete')")
	require.NoError(t, err)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	target := transferDB(t, Config{Type: TypeSQLite})
	err = target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), RelationalImportIdentity{OperationID: "new", RestrictionID: "closed"}, restrictImportedFixture)
	require.ErrorIs(t, err, ErrImportRestricted)
	require.NoError(t, target.CheckImportBarrier(t.Context()))
	var count int
	require.NoError(t, target.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users").Scan(&count))
	require.Zero(t, count)
}

func TestRelationalImportBarrierRejectsMalformedMarkers(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			require.NoError(t, target.CheckImportBarrier(t.Context()))
			_, err := target.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES('relational-import-v1','')")
			require.NoError(t, err)
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
		})
	}
}
