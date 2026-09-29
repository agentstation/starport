package sqlstore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelationalImportInspection(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	backup, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "source"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			identity := RelationalImportIdentity{OperationID: "inspection", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "source", "starport.db"), backup.Snapshot, transferDirectory(t), identity, restrictImportedFixture))
			inspect := func(position RelationalReplayPosition) (SQLiteSnapshotResult, error) {
				return target.SnapshotRelationalImport(t.Context(), filepath.Join(transferDirectory(t), "inspected"), backup.Snapshot, identity, position)
			}
			result, err := inspect(RelationalReplayPosition{})
			require.NoError(t, err)
			require.NotEmpty(t, result.Snapshot.SHA256)
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), ErrImportRestricted)
			step := RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: strings.Repeat("b", 64)}
			digest, err := target.ReplayRelationalImport(t.Context(), backup.Snapshot, identity, step, func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=epoch+10 WHERE deployment_id='deployment'")
				return err
			})
			require.NoError(t, err)
			position := RelationalReplayPosition{Sequence: 1, ReceiptSHA256: digest}
			require.NoError(t, target.CheckRelationalImportPosition(t.Context(), backup.Snapshot, identity, position))
			result, err = inspect(position)
			require.NoError(t, err)
			require.NotEmpty(t, result.Snapshot.SHA256)
			output := filepath.Join(transferDirectory(t), "readable")
			result, err = target.SnapshotRelationalImport(t.Context(), output, backup.Snapshot, identity, position)
			require.NoError(t, err)
			view, err := OpenRelationalSnapshot(t.Context(), filepath.Join(output, "starport.db"), result.Snapshot, transferDirectory(t))
			require.NoError(t, err)
			var expected, actual int64
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT epoch FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&expected))
			require.NoError(t, view.QueryRowContext(t.Context(), "SELECT epoch FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&actual))
			require.Equal(t, expected, actual)
			require.NoError(t, view.Close())
			for _, invalid := range []RelationalReplayPosition{{}, {Sequence: -1}, {Sequence: 1}, {ReceiptSHA256: digest}, {Sequence: 2, ReceiptSHA256: digest}, {Sequence: 1, ReceiptSHA256: strings.Repeat("c", 64)}} {
				result, err := inspect(invalid)
				require.ErrorIs(t, err, ErrImportRestricted)
				require.Empty(t, result.Snapshot.SHA256)
			}
			changed := identity
			changed.OperationID = "different"
			require.ErrorIs(t, target.CheckRelationalImportPosition(t.Context(), backup.Snapshot, changed, position), ErrImportRestricted)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			require.ErrorIs(t, target.CheckRelationalImportPosition(ctx, backup.Snapshot, identity, position), context.Canceled)
			var saved string
			require.NoError(t, target.QueryRowContext(t.Context(), target.Bind("SELECT value FROM sqlstore_meta WHERE name=?"), relationalReplayCurrent).Scan(&saved))
			for _, corrupt := range []string{"{}", strings.Repeat("x", 1025), strings.TrimSuffix(saved, "}") + `,"unknown":true}`} {
				_, err := target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value=? WHERE name=?"), corrupt, relationalReplayCurrent)
				require.NoError(t, err)
				path := filepath.Join(transferDirectory(t), "refused")
				_, err = target.SnapshotRelationalImport(t.Context(), path, backup.Snapshot, identity, position)
				require.ErrorIs(t, err, ErrImportRestricted)
				_, err = os.Stat(path)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			_, err = target.ExecContext(t.Context(), target.Bind("UPDATE sqlstore_meta SET value=? WHERE name=?"), saved, relationalReplayCurrent)
			require.NoError(t, err)
			require.NoError(t, target.ActivateRelationalImport(t.Context(), backup.Snapshot, identity, strings.Repeat("d", 64), func(context.Context, *sql.Conn) error { return nil }))
			_, err = inspect(position)
			require.ErrorIs(t, err, ErrImportRestricted)
		})
	}
}
