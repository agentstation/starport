package config

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func storageByID(stores []StorageLifetime) map[string]StorageLifetime {
	byID := make(map[string]StorageLifetime, len(stores))
	for _, store := range stores {
		byID[store.ID] = store
	}
	return byID
}

func TestEffectiveReportCarriesPathsAndLocalStorageLifetimes(t *testing.T) {
	paths := PathsForConfigDir(filepath.Join(t.TempDir(), "not-created"))
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)

	report := cfg.EffectiveReport()
	require.Equal(t, cfg.EffectivePaths(), report.Paths)
	require.NotEmpty(t, report.Paths.DeploymentID)
	require.NoError(t, cfg.CheckWriteRequest(report.Paths.DeploymentID), "a save names the reported deployment")
	stores := storageByID(report.Storage)
	require.Equal(t, StorageLifetime{ID: "kv", Selection: storageModeBadger, Lifetime: LifetimeLocal, Location: report.Paths.BadgerDir}, stores["kv"])
	require.Equal(t, StorageLifetime{ID: "sql", Selection: sqlModeSQLite, Lifetime: LifetimeLocal, Location: report.Paths.SQLiteFile}, stores["sql"])
	require.Equal(t, StorageLifetime{ID: "blobs", Selection: BlobBackendFilesystem, Lifetime: LifetimeLocal, Location: report.Paths.FilesDir}, stores["blobs"])
	require.NoDirExists(t, paths.ConfigDir)
}

func TestEffectiveReportStorageLifetimesOmitServiceEndpointsAndCredentials(t *testing.T) {
	paths := PathsForConfigDir(filepath.Join(t.TempDir(), "not-created"))
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles().Load(t.Context(), func(cfg *Config) {
		cfg.Storage.Mode = storageModeValkey
		cfg.Storage.Valkey.URL = "redis://account:kv-secret@127.0.0.1:1"
		cfg.Storage.SQL.Mode = sqlModePostgres
		cfg.Storage.SQL.Postgres.URL = "postgres://account:sql-secret@127.0.0.1:1/db"
		cfg.Files.Backend = BlobBackendObjectStore
		cfg.Files.ObjectStore.Bucket = "private-bucket"
		cfg.Files.ObjectStore.Region = "us-east-1"
		cfg.Files.ObjectStore.AccessKeyID = "private-access-key"
		cfg.Files.ObjectStore.SecretAccessKey = "private-secret-key"
	})
	require.NoError(t, err)

	report := cfg.EffectiveReport()
	require.Equal(t, []StorageLifetime{
		{ID: "kv", Selection: storageModeValkey, Lifetime: LifetimeService},
		{ID: "sql", Selection: sqlModePostgres, Lifetime: LifetimeService},
		{ID: "blobs", Selection: BlobBackendObjectStore, Lifetime: LifetimeService},
	}, report.Storage)
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	for _, secret := range []string{"kv-secret", "sql-secret", "127.0.0.1", "private-bucket", "private-access-key", "private-secret-key"} {
		require.NotContains(t, string(encoded), secret)
	}
}

func TestEffectiveReportDevelopmentStorageEndsWithTheProcess(t *testing.T) {
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(nil).LoadDevelopment(t.Context())
	require.NoError(t, err)

	for _, store := range cfg.EffectiveReport().Storage {
		require.Equal(t, LifetimeProcess, store.Lifetime, store.ID)
	}
	stores := storageByID(cfg.StorageLifetimes())
	require.Equal(t, selectionProcessMemory, stores["kv"].Selection)
	require.Equal(t, selectionProcessMemory, stores["sql"].Selection)
	require.Equal(t, BlobBackendFilesystem, stores["blobs"].Selection)
}
