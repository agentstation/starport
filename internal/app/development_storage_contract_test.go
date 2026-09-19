package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestDevelopmentDatabasesAreSessionLocal(t *testing.T) {
	installation := filepath.Join(t.TempDir(), "installation")
	for _, name := range []string{"config/config.env", "data/badger/operator", "data/sqlite/starport.db", "state/operator"} {
		path := filepath.Join(installation, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte("existing installation: "+name), 0600))
	}
	before := developmentInstallationFiles(t, installation)
	for session := range 2 {
		cfg, err := config.NewLoader().WithEnvFiles().WithEnvironment(map[string]string{
			"STARPORT_HOME":                        installation,
			"STARPORT_CATALOG_NETWORK_MODE":        "offline",
			"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
		}).LoadDevelopment(t.Context())
		require.NoError(t, err)
		var database *sqlstore.DB
		runtime, err := NewDevelopment(t.Context(), cfg, func(options *buildOptions) {
			original := options.factories.openSQL
			options.factories.openSQL = func(settings config.StorageConfig) (*sqlstore.DB, error) {
				opened, err := original(settings)
				database = opened
				return opened, err
			}
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
		require.NotNil(t, database)
		require.Equal(t, sqlstore.TypeSQLite, database.Dialect())
		rows, err := database.QueryContext(t.Context(), "PRAGMA database_list")
		require.NoError(t, err)
		count := 0
		for rows.Next() {
			var sequence int
			var name, path string
			require.NoError(t, rows.Scan(&sequence, &name, &path))
			require.Empty(t, path, "development SQL must have no backing file")
			count++
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		require.Positive(t, count)
		var tables int
		require.NoError(t, database.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name = 'development_session_probe'").Scan(&tables))
		require.Zero(t, tables, "a new session must not inherit SQL data")
		_, err = database.ExecContext(t.Context(), "CREATE TABLE development_session_probe (value TEXT NOT NULL)")
		require.NoError(t, err)
		_, err = database.ExecContext(t.Context(), "INSERT INTO development_session_probe VALUES ('ephemeral')")
		require.NoError(t, err)
		var value string
		require.NoError(t, database.QueryRowContext(t.Context(), "SELECT value FROM development_session_probe").Scan(&value))
		require.Equal(t, "ephemeral", value)
		_, err = runtime.application.store.Get(t.Context(), "development-session-probe")
		require.ErrorIs(t, err, storage.ErrNotFound)
		require.NoError(t, runtime.application.store.Set(t.Context(), "development-session-probe", []byte("ephemeral")))
		got, err := runtime.application.store.Get(t.Context(), "development-session-probe")
		require.NoError(t, err)
		require.Equal(t, []byte("ephemeral"), got)
		require.True(t, filepath.IsLocal(mustRelative(t, runtime.scratchRoot, cfg.Files.Path)))
		_, err = runtime.application.blobStore.Put(t.Context(), "session-probe", strings.NewReader("ephemeral blob bytes"))
		require.NoError(t, err)
		blobFiles := developmentInstallationFiles(t, cfg.Files.Path)
		storedBlobs := 0
		for _, contents := range blobFiles {
			if contents == "ephemeral blob bytes" {
				storedBlobs++
			}
		}
		require.Equal(t, 1, storedBlobs)
		require.Equal(t, before, developmentInstallationFiles(t, installation), "session %d changed persistent files", session)
		require.NoError(t, runtime.Close(t.Context()))
		require.NoDirExists(t, runtime.scratchRoot)
		require.Equal(t, before, developmentInstallationFiles(t, installation))
	}
}

func developmentInstallationFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(contents)
		return nil
	}))
	return files
}
