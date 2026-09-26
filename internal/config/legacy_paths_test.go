package config

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/stretchr/testify/require"
)

func legacyPathsFixture(t *testing.T) Paths {
	t.Helper()
	paths := PathsForConfigDir(filepath.Join(t.TempDir(), "old-config"))
	paths.DataDir = filepath.Join(t.TempDir(), "new-data")
	paths.BadgerDir = filepath.Join(paths.DataDir, "badger")
	paths.SQLiteFile = filepath.Join(paths.DataDir, "sqlite", "starport.db")
	paths.FilesDir = filepath.Join(paths.DataDir, "files")
	paths.LocalTokenFile = filepath.Join(paths.DataDir, "local-admin-token.json")
	paths.WelcomeStampFile = filepath.Join(paths.DataDir, "welcomed")
	paths.Origins["data"] = productpaths.Path{Path: paths.DataDir, Origin: "platform-default"}
	return paths
}

func TestLegacyPathsRefuseOldAndConflictingDataWithoutChanges(t *testing.T) {
	for _, name := range []string{"badger", "sqlite/starport.db", "sqlite/starport.db-wal", "files", "local-admin-token.json", "welcomed"} {
		t.Run(name, func(t *testing.T) {
			paths := legacyPathsFixture(t)
			previous := filepath.Join(paths.ConfigDir, "data", filepath.FromSlash(name))
			require.NoError(t, os.MkdirAll(filepath.Dir(previous), 0o700))
			require.NoError(t, os.WriteFile(previous, []byte("private retained data"), 0o600))
			for _, populatedTarget := range []bool{false, true} {
				if populatedTarget {
					require.NoError(t, os.MkdirAll(paths.DataDir, 0o700))
					require.NoError(t, os.WriteFile(filepath.Join(paths.DataDir, "unrelated"), []byte("preserve"), 0o600))
				}
				require.ErrorIs(t, paths.CheckLegacyPaths(t.Context()), ErrLegacyPaths)
				contents, err := os.ReadFile(previous)
				require.NoError(t, err)
				require.Equal(t, "private retained data", string(contents))
			}
		})
	}
}

func TestLegacyPathsPreserveExplicitRootsAndLeaves(t *testing.T) {
	paths := legacyPathsFixture(t)
	previous := filepath.Join(paths.ConfigDir, "data", "badger")
	require.NoError(t, os.MkdirAll(previous, 0o700))
	require.ErrorIs(t, paths.CheckLegacyPaths(t.Context()), ErrLegacyPaths)
	paths.Origins["badger"] = productpaths.Path{Path: paths.BadgerDir, Origin: "environment"}
	require.NoError(t, paths.CheckLegacyPaths(t.Context()))
	delete(paths.Origins, "badger")
	paths.Origins["data"] = productpaths.Path{Path: paths.DataDir, Origin: "environment"}
	require.NoError(t, paths.CheckLegacyPaths(t.Context()))
	paths = PathsForConfigDir(paths.ConfigDir)
	require.NoError(t, paths.CheckLegacyPaths(t.Context()))
}

func TestLegacyRuntimeUsesMarkersAndHonorsExplicitSelection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	paths := legacyPathsFixture(t)
	paths.StateDir = filepath.Join(root, "starport")
	paths.RuntimeDir = filepath.Join(paths.StateDir, "catalog", "runtime", "default")
	paths.Origins["state"] = productpaths.Path{Path: paths.StateDir, Origin: "platform-default"}
	require.NoError(t, os.MkdirAll(paths.RuntimeDir, 0o700))
	require.NoError(t, paths.CheckLegacyPaths(t.Context()))
	previous := filepath.Join(root, "starport", "catalog", "instance-seed")
	require.NoError(t, os.WriteFile(previous, []byte("private old seed"), 0o600))
	require.ErrorIs(t, paths.CheckLegacyPaths(t.Context()), ErrLegacyPaths)
	paths.Origins[pathRoleRuntime] = productpaths.Path{Path: paths.RuntimeDir, Origin: "environment"}
	require.NoError(t, paths.CheckLegacyPaths(t.Context()))
}

func TestLegacyConfigurationUsesPreviousNativeLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	previousRoot, err := os.UserConfigDir()
	require.NoError(t, err)
	previous := filepath.Join(previousRoot, "starport", "config.env")
	require.NoError(t, os.MkdirAll(filepath.Dir(previous), 0o700))
	require.NoError(t, os.WriteFile(previous, []byte("do not read these credentials"), 0o600))
	root, err := productpaths.UserDefaults(productpaths.Starport)(productpaths.Config)
	require.NoError(t, err)
	paths := PathsForConfigDir(root)
	paths.Origins["config"] = productpaths.Path{Path: root, Origin: "platform-default"}
	if runtime.GOOS == "darwin" {
		require.ErrorIs(t, paths.CheckLegacyPaths(t.Context()), ErrLegacyPaths)
		require.NoDirExists(t, root)
	} else {
		require.Equal(t, previous, paths.ConfigFile)
		require.NoError(t, paths.CheckLegacyPaths(t.Context()))
	}
	paths.configExplicit = true
	require.NoError(t, paths.CheckLegacyPaths(t.Context()))
}

func TestLegacyInspectionSkipsUnselectedStoresAndDevelopment(t *testing.T) {
	paths := legacyPathsFixture(t)
	for _, path := range []string{"badger", "sqlite/starport.db-wal", "files"} {
		previous := filepath.Join(paths.ConfigDir, "data", filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(previous), 0o700))
		require.NoError(t, os.WriteFile(previous, []byte("retained"), 0o600))
	}
	cfg := &Config{paths: paths}
	cfg.Storage.Mode, cfg.Storage.SQL.Mode, cfg.Files.Backend = storageModeValkey, sqlModePostgres, BlobBackendObjectStore
	conflicts, err := cfg.LegacyPaths(t.Context())
	require.NoError(t, err)
	require.Empty(t, conflicts)
	cfg.Storage.Mode, cfg.Storage.SQL.Mode, cfg.Files.Backend = storageModeBadger, sqlModeSQLite, BlobBackendFilesystem
	conflicts, err = cfg.LegacyPaths(t.Context())
	require.NoError(t, err)
	require.Len(t, conflicts, 3)
	cfg.Catalog.stateDirectoryScratch = true
	require.NoError(t, cfg.CheckLegacyPaths(t.Context()))
}

func TestLegacyInspectionPreservesCancellationAndLinks(t *testing.T) {
	paths := legacyPathsFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, paths.CheckLegacyPaths(ctx), context.Canceled)
	previous := filepath.Join(paths.ConfigDir, "data", "badger")
	require.NoError(t, os.MkdirAll(filepath.Dir(previous), 0o700))
	if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), previous); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	require.ErrorIs(t, paths.CheckLegacyPaths(t.Context()), ErrLegacyPaths)
	info, err := os.Lstat(previous)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}
