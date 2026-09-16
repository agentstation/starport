package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starmap/pkg/productpaths/policy"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/stretchr/testify/require"
)

func manifestEntry(t *testing.T, report productpaths.FileManifest, id string) productpaths.FileEntry {
	t.Helper()
	for _, entry := range report.Files {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("missing file role %s", id)
	return productpaths.FileEntry{}
}

func TestFileManifestUsesConfigurationReadPolicy(t *testing.T) {
	for _, selected := range []string{policy.OwnerOnly, policy.ServiceManaged} {
		t.Run(selected, func(t *testing.T) {
			paths := PathsForConfigDir(t.TempDir())
			require.NoError(t, os.WriteFile(paths.ConfigFile, []byte("STARPORT_CONFIG_ACCESS=service-managed\n"), 0o600))
			cfg, err := NewLoader().WithPaths(paths).WithEnvironment(map[string]string{
				"STARPORT_CONFIG_FILE": paths.ConfigFile, "STARPORT_CONFIG_ACCESS": selected,
			}).Load(t.Context())
			require.NoError(t, err)
			report, err := cfg.FileManifest("test")
			require.NoError(t, err)
			require.Equal(t, selected, manifestEntry(t, report, "configuration").Policy.Access)
		})
	}
}

func TestFileManifestReportsOnlySelectedConfigurationFiles(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	file := filepath.Join(paths.ConfigDir, "explicit.env")
	require.NoError(t, os.WriteFile(file, []byte("STARPORT_RELATIVE_PATH_BASE=config\nSTARPORT_STORAGE_BADGER_PATH=kv\nSTARPORT_SECURITY_MASTER_KEY=never-report-this-key-with-32-bytes\n"), 0o600))
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles(file).Load(t.Context())
	require.NoError(t, err)
	report, err := cfg.FileManifest("test")
	require.NoError(t, err)
	require.Equal(t, fileDisabled, manifestEntry(t, report, "configuration").Availability)
	dotenv := manifestEntry(t, report, "dotenv-0")
	require.Equal(t, file, dotenv.Location.Path)
	require.Equal(t, policy.OwnerOnly, dotenv.Policy.Access)
	badger := manifestEntry(t, report, "badger")
	require.Equal(t, filepath.Join(paths.ConfigDir, "kv"), badger.Location.Path)
	require.Equal(t, "file:"+file, badger.Location.Origin)
	require.Equal(t, paths.ConfigDir, badger.Location.Anchor)
	require.Equal(t, "config", report.RelativePathBase)
	require.Equal(t, "file:"+file, report.RelativePathBaseOrigin)
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "never-report-this-key-with-32-bytes")
	require.NoFileExists(t, paths.ConfigFile)
}

func TestFileManifestSharedBackendsOmitCredentialsAndLocalSelection(t *testing.T) {
	paths := PathsForConfigDir(filepath.Join(t.TempDir(), "not-created"))
	for _, sql := range []string{sqlModePostgres, sqlModeMySQL} {
		t.Run(sql, func(t *testing.T) {
			cfg, err := NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles().Load(t.Context(), func(cfg *Config) {
				cfg.Storage.Mode = storageModeValkey
				cfg.Storage.Valkey.URL = "redis://account:kv-secret@127.0.0.1:1"
				cfg.Storage.SQL.Mode = sql
				cfg.Storage.SQL.Postgres.URL = "postgres://account:sql-secret@127.0.0.1:1/db"
				cfg.Storage.SQL.MySQL.DSN = "account:sql-secret@tcp(127.0.0.1:1)/db"
				cfg.Files.Backend = BlobBackendObjectStore
				cfg.Files.ObjectStore.Bucket = "private-bucket"
				cfg.Files.ObjectStore.Region = "us-east-1"
				cfg.Files.ObjectStore.AccessKeyID = "private-access-key"
				cfg.Files.ObjectStore.SecretAccessKey = "private-secret-key"
			})
			require.NoError(t, err)
			report, err := cfg.FileManifest("test")
			require.NoError(t, err)
			for _, role := range []string{"badger", "sqlite", "sqlite-wal", "files"} {
				require.Equal(t, fileDisabled, manifestEntry(t, report, role).Availability)
			}
			external := make(map[string]string)
			for _, entry := range report.External {
				external[entry.ID] = entry.Selection
			}
			require.Equal(t, "valkey", external["kv"])
			require.Equal(t, sql, external["sql"])
			require.Equal(t, BlobBackendObjectStore, external["blobs"])
			encoded, err := json.Marshal(report)
			require.NoError(t, err)
			for _, secret := range []string{"kv-secret", "sql-secret", "private-bucket", "private-access-key", "private-secret-key"} {
				require.NotContains(t, string(encoded), secret)
			}
			require.NoDirExists(t, paths.ConfigDir)
		})
	}
}

func TestFileManifestDevelopmentDisablesPersistentStores(t *testing.T) {
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(nil).LoadDevelopment(t.Context())
	require.NoError(t, err)
	report, err := cfg.FileManifest("test")
	require.NoError(t, err)
	for _, role := range []string{"configuration", "badger", "sqlite", "baseline", "runtime-evidence", "welcome-stamp"} {
		require.Equal(t, fileDisabled, manifestEntry(t, report, role).Availability)
	}
	for _, entry := range report.External {
		if entry.ID == "kv" || entry.ID == "sql" {
			require.Equal(t, "process-memory", entry.Selection)
		}
	}
}

func TestFileManifestSourceCachesFollowCanonicalSelection(t *testing.T) {
	for _, test := range []struct {
		name      string
		value     *string
		http, git string
	}{
		{"default", nil, fileAvailable, fileDisabled},
		{"disabled", new(""), fileDisabled, fileDisabled},
		{"git", new(string(sources.ModelsDevGitID)), fileDisabled, fileAvailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := PathsForConfigDir(t.TempDir())
			env := map[string]string{}
			if test.value != nil {
				env["STARPORT_CATALOG_ACQUISITION_SOURCES"] = *test.value
			}
			cfg, err := NewLoader().WithPaths(paths).WithEnvironment(env).WithEnvFiles().Load(t.Context())
			require.NoError(t, err)
			report, err := cfg.FileManifest("test")
			require.NoError(t, err)
			http := manifestEntry(t, report, "source-http")
			git := manifestEntry(t, report, "source-checkout")
			require.Equal(t, filepath.Join(paths.CacheDir, "models.dev"), http.Location.Path)
			require.Equal(t, filepath.Join(paths.CacheDir, "sources", "models.dev-git"), git.Location.Path)
			require.Equal(t, test.http, http.Availability)
			require.Equal(t, test.git, git.Availability)
			require.Equal(t, policy.DeploymentControlled, http.Policy.Access)
			require.NoDirExists(t, paths.CacheDir)
		})
	}
}
