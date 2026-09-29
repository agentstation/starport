package config

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/stretchr/testify/require"
)

func backupInventoryConfig(t *testing.T, environment map[string]string) *Config {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	_, err := productfiles.CreateDirectory(root)
	require.NoError(t, err)
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(root)).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	return cfg
}

func writeInventoryFixture(t *testing.T, name, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o700))
	require.NoError(t, os.WriteFile(name, []byte(body), 0o600))
}

func TestBackupInventoryAccountsForEveryCanonicalRole(t *testing.T) {
	cfg := backupInventoryConfig(t, nil)
	paths := cfg.EffectivePaths()
	writeInventoryFixture(t, filepath.Join(paths.RuntimeDir, "nested", "evidence.json"), "runtime")
	writeInventoryFixture(t, filepath.Join(paths.BaselineDir, "generation", "catalog.json"), "baseline")
	writeInventoryFixture(t, filepath.Join(cfg.CatalogCredentialPolicyDirectory(), "accepted.json"), "acquisition")
	writeInventoryFixture(t, filepath.Join(cfg.InferenceCredentialPolicyDirectory(), "accepted.json"), "inference")
	writeInventoryFixture(t, paths.LocalTokenFile, "private token")
	writeInventoryFixture(t, filepath.Join(paths.BadgerDir, "engine-only"), "must use native KV snapshot")
	writeInventoryFixture(t, paths.SQLiteFile, "must use relational snapshot")
	writeInventoryFixture(t, filepath.Join(paths.FilesDir, "engine-only"), "must use blob snapshot")
	inventory, err := cfg.CollectBackupInventory(t.Context(), "test", 1000)
	require.NoError(t, err)
	canonical, err := cfg.FileManifest("test")
	require.NoError(t, err)
	require.Len(t, inventory.Roles, len(canonical.Files))
	captures := map[string]string{}
	for _, role := range inventory.Roles {
		captures[role.Entry.ID] = role.Capture
	}
	require.Equal(t, "kv-snapshot", captures["badger"])
	require.Equal(t, "sql-snapshot", captures["sqlite"])
	require.Equal(t, "blob-snapshot", captures["files"])
	require.Equal(t, "rebuild-under-source-policy", captures["source-http"])
	require.Equal(t, "not-selected", captures["logs"])
	sourcePaths := map[string]bool{}
	for _, file := range inventory.Files {
		sourcePaths[file.Source] = true
		require.True(t, strings.HasPrefix(file.ArtifactID, "inventory/"))
		require.Len(t, file.ArtifactID, len("inventory/")+64)
	}
	require.Len(t, sourcePaths, 5)
	require.True(t, sourcePaths[paths.LocalTokenFile])
	require.True(t, sourcePaths[filepath.Join(paths.RuntimeDir, "nested", "evidence.json")])
	encoded, err := json.Marshal(inventory)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private token")
	require.NotContains(t, string(encoded), "must use native KV snapshot")
}

func TestBackupInventoryTracksLoadedConfigurationDigest(t *testing.T) {
	initial := backupInventoryConfig(t, nil)
	configFile := initial.EffectivePaths().ConfigFile
	writeInventoryFixture(t, configFile, "STARPORT_LOG_LEVEL=info\n")
	cfg, err := NewLoader().WithPaths(initial.EffectivePaths()).WithEnvironment(map[string]string{"STARPORT_CONFIG_FILE": configFile}).Load(t.Context())
	require.NoError(t, err)
	inventory, err := cfg.CollectBackupInventory(t.Context(), "test", 1000)
	require.NoError(t, err)
	require.Len(t, inventory.Files, 1)
	require.Equal(t, "configuration", inventory.Files[0].Role)
	require.Len(t, inventory.Files[0].ExpectedSHA256, 64)
	require.NoError(t, os.Remove(configFile))
	_, err = cfg.CollectBackupInventory(t.Context(), "test", 1000)
	require.ErrorContains(t, err, "selected configuration")
}

func TestBackupInventoryPreservesSelectedWorkspaceAndRecoveryFiles(t *testing.T) {
	cfg := backupInventoryConfig(t, nil)
	parent := cfg.EffectivePaths().ConfigDir
	cfg.Catalog.WorkspacePath = filepath.Join(parent, "workspace[local]")
	writeInventoryFixture(t, filepath.Join(cfg.Catalog.WorkspacePath, "模型.yaml"), "selected workspace")
	writeInventoryFixture(t, filepath.Join(parent, ".workspace[local].backup-one", "models.yaml"), "selected backup")
	writeInventoryFixture(t, filepath.Join(parent, ".different.backup-one", "models.yaml"), "unrelated backup")
	inventory, err := cfg.CollectBackupInventory(t.Context(), "test", 1000)
	require.NoError(t, err)
	require.Len(t, inventory.Files, 2)
	relative := map[string]string{}
	for _, file := range inventory.Files {
		relative[file.Role] = file.Relative
	}
	require.Equal(t, "模型.yaml", relative["workspace"])
	require.Equal(t, ".workspace[local].backup-one/models.yaml", relative["workspace-backup"])
}

func TestBackupInventoryRefusesIncompleteOrUnsafeScan(t *testing.T) {
	for _, kind := range []string{"limit", "symlink", "wrong-type"} {
		t.Run(kind, func(t *testing.T) {
			cfg := backupInventoryConfig(t, nil)
			file := filepath.Join(cfg.EffectivePaths().RuntimeDir, "record.json")
			writeInventoryFixture(t, file, "original")
			limit := 1000
			switch kind {
			case "limit":
				limit = 1
			case "symlink":
				require.NoError(t, os.Remove(file))
				elsewhere := filepath.Join(t.TempDir(), "outside")
				writeInventoryFixture(t, elsewhere, "outside")
				require.NoError(t, os.Symlink(elsewhere, file))
			case "wrong-type":
				require.NoError(t, os.MkdirAll(cfg.EffectivePaths().LocalTokenFile, 0o700))
			}
			_, err := cfg.CollectBackupInventory(t.Context(), "test", limit)
			require.Error(t, err)
		})
	}
}

func TestBackupInventoryRefusesUnknownActiveRole(t *testing.T) {
	_, err := backupRoleCapture(productpaths.FileEntry{ID: "new-store", Availability: "available"})
	require.ErrorContains(t, err, "no capture rule")
}

func TestBackupInventoryAllowsAbsentDefaultConfiguration(t *testing.T) {
	initial := backupInventoryConfig(t, nil)
	cfg, err := NewLoader().WithPaths(initial.EffectivePaths()).WithEnvironment(nil).Load(t.Context())
	require.NoError(t, err)
	inventory, err := cfg.CollectBackupInventory(t.Context(), "test", 1000)
	require.NoError(t, err)
	require.Empty(t, inventory.Files)
	require.NoFileExists(t, cfg.EffectivePaths().ConfigFile)
}
