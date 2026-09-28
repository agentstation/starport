package sqlstore

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelationalSnapshotViewIsReadOnlyAndOwnsItsCopy(t *testing.T) {
	db, parent := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, db)
	snapshot, err := db.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	source := filepath.Join(parent, "snapshot", "starport.db")
	scratch := transferDirectory(t)
	view, err := OpenRelationalSnapshot(t.Context(), source, snapshot.Snapshot, scratch)
	require.NoError(t, err)
	var count int
	require.NoError(t, view.QueryRowContext(t.Context(), "SELECT count(*) FROM users").Scan(&count))
	require.Equal(t, 1, count)
	rows, err := view.QueryContext(t.Context(), "DELETE FROM users")
	if err == nil {
		for rows.Next() {
		}
		err = rows.Err()
		require.NoError(t, rows.Close())
	}
	require.Error(t, err, "even query-shaped write attempts must fail")
	require.NoError(t, view.QueryRowContext(t.Context(), "SELECT count(*) FROM users").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, view.Close())
	require.FileExists(t, source)
	entries, err := filepath.Glob(filepath.Join(scratch, ".relational-view-*"))
	require.NoError(t, err)
	require.Empty(t, entries)
	wrong := snapshot.Snapshot
	wrong.SHA256 = strings.Repeat("0", 64)
	_, err = OpenRelationalSnapshot(t.Context(), source, wrong, scratch)
	require.Error(t, err)
}
