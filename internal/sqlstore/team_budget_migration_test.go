package sqlstore

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestTeamBudgetMigrationDoesNotGrantZeroHistory(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			db, err := Open(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			previous := fstest.MapFS{}
			err = fs.WalkDir(migrations, "migrations", func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() || strings.HasSuffix(path, "0011_team_budget_origins.sql") {
					return err
				}
				data, err := fs.ReadFile(migrations, path)
				previous[path] = &fstest.MapFile{Data: data}
				return err
			})
			require.NoError(t, err)
			require.NoError(t, db.migrate(t.Context(), previous))
			_, err = db.ExecContext(t.Context(), `INSERT INTO teams (id, revision, record) VALUES ('existing-team', 1, '{}')`)
			require.NoError(t, err)
			require.NoError(t, db.Migrate(t.Context()))
			require.NoError(t, db.Migrate(t.Context()))
			var allowed int
			var history string
			err = db.QueryRowContext(t.Context(), `SELECT history_id, initialize_allowed FROM team_budget_origins WHERE team_id = 'existing-team'`).Scan(&history, &allowed)
			require.NoError(t, err)
			require.Zero(t, allowed)
			require.Empty(t, history)
			_, err = db.ExecContext(t.Context(), `DELETE FROM teams WHERE id = 'existing-team'`)
			require.NoError(t, err)
			err = db.QueryRowContext(t.Context(), `SELECT initialize_allowed FROM team_budget_origins WHERE team_id = 'existing-team'`).Scan(&allowed)
			require.NoError(t, err)
			require.Zero(t, allowed, "deletion must retain the denied origin")
		})
	}
}
