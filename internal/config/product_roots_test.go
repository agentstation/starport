package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoaderOwnsIndependentProductRoots(t *testing.T) {
	for _, overrideData := range []bool{false, true} {
		t.Run(map[bool]string{false: "grouped home", true: "specific data root"}[overrideData], func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "starport")
			data := filepath.Join(home, "data")
			environment := map[string]string{
				"STARPORT_HOME":        home,
				"STARPORT_INSTANCE_ID": "gateway-one",
				"STARMAP_HOME":         filepath.Join(t.TempDir(), "other-product"),
				"STARMAP_STATE_DIR":    filepath.Join(t.TempDir(), "other-state"),
			}
			if overrideData {
				data = filepath.Join(t.TempDir(), "durable")
				environment["STARPORT_DATA_DIR"] = data
			}
			cfg, err := NewLoader().WithEnvironment(environment).WithEnvFiles().Load(t.Context())
			require.NoError(t, err)
			require.Equal(t, filepath.Join(data, "badger"), cfg.Storage.Badger.Path)
			require.Equal(t, filepath.Join(data, "sqlite", "starport.db"), cfg.Storage.SQL.SQLite.Path)
			require.Equal(t, filepath.Join(data, "files"), cfg.Files.Path)
			require.Equal(t, filepath.Join(home, "state", "catalog", "runtime", "gateway-one"), cfg.Catalog.StateDirectory)
		})
	}
}

func TestLoaderRejectsInvalidRootAndInstanceSelections(t *testing.T) {
	for _, test := range []struct {
		name, setting, value string
	}{
		{"relative root", "STARPORT_DATA_DIR", "relative/data"},
		{"empty root", "STARPORT_DATA_DIR", ""},
		{"unsafe instance", "STARPORT_INSTANCE_ID", "../shared"},
		{"reserved instance", "STARPORT_INSTANCE_ID", "con"},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := map[string]string{"STARPORT_HOME": t.TempDir(), test.setting: test.value}
			_, err := NewLoader().WithEnvironment(environment).WithEnvFiles().Load(t.Context())
			require.Error(t, err)
		})
	}
}

func TestLoaderRootSpecificityAndBootstrapSelection(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	dataDir := filepath.Join(t.TempDir(), "data")
	file := filepath.Join(configDir, "config.env")
	require.NoError(t, os.WriteFile(file, []byte("STARPORT_DATA_DIR="+dataDir+"\nSTARPORT_CONFIG_DIR=/must-not-relocate\nSTARPORT_SERVER_PORT=9191\n"), 0o600))
	cfg, err := NewLoader().WithEnvironment(map[string]string{"STARPORT_HOME": home}).Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dataDir, "badger"), cfg.Storage.Badger.Path)
	require.Equal(t, 9191, cfg.Server.Port)
}

func TestLoaderRequiresExplicitRelativePathBase(t *testing.T) {
	for _, setting := range []string{"STARPORT_STORAGE_BADGER_PATH", "STARPORT_STORAGE_SQL_SQLITE_PATH", "STARPORT_CATALOG_STATE_DIR", "STARPORT_CATALOG_WORKSPACE_PATH"} {
		t.Run(setting, func(t *testing.T) {
			paths := PathsForConfigDir(t.TempDir())
			environment := map[string]string{setting: "relative"}
			_, err := NewLoader().WithPaths(paths).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
			require.Error(t, err)
			environment["STARPORT_RELATIVE_PATH_BASE"] = "config"
			_, err = NewLoader().WithPaths(paths).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
			require.NoError(t, err)
		})
	}
}

func TestLoaderRejectsOversizedConfiguration(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	require.NoError(t, os.WriteFile(paths.ConfigFile, []byte("#"+strings.Repeat("x", 1<<20)), 0o600))
	_, err := NewLoader().WithPaths(paths).WithEnvironment(nil).Load(t.Context())
	require.Error(t, err)
}

func TestLoaderRejectsMissingExplicitConfiguration(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	_, err := NewLoader().WithPaths(paths).WithEnvironment(map[string]string{"STARPORT_CONFIG_FILE": filepath.Join(paths.ConfigDir, "missing.env")}).Load(t.Context())
	require.Error(t, err)
}

func TestLoaderPrimaryFileCannotAuthorizeItsOwnRelativeSelection(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	require.NoError(t, os.WriteFile(paths.ConfigFile, []byte("STARPORT_RELATIVE_PATH_BASE=config\nSTARPORT_SERVER_PORT=9191\n"), 0o600))
	env := map[string]string{"STARPORT_CONFIG_FILE": "config.env"}
	_, err := NewLoader().WithPaths(paths).WithEnvironment(env).Load(t.Context())
	require.Error(t, err)
	env["STARPORT_RELATIVE_PATH_BASE"] = "config"
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(env).Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, 9191, cfg.Server.Port)
	require.Equal(t, paths.ConfigDir, cfg.EffectivePaths().Origins["configuration"].Anchor)
}

func TestLoaderRejectsEmptyPersistentLeaves(t *testing.T) {
	for _, setting := range []string{"STARPORT_STORAGE_BADGER_PATH", "STARPORT_STORAGE_SQL_SQLITE_PATH", "STARPORT_CATALOG_STATE_DIR", "STARPORT_FILES_PATH"} {
		t.Run(setting, func(t *testing.T) {
			_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{setting: ""}).WithEnvFiles().Load(t.Context())
			require.Error(t, err)
		})
	}
}
