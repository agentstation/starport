package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func productSetupPaths(t *testing.T) config.Paths {
	t.Helper()
	cfg, err := config.NewLoader().WithEnvironment(map[string]string{"STARPORT_HOME": filepath.Join(t.TempDir(), "gateway")}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	return cfg.EffectivePaths()
}

func TestInitializeSeparateProductRoots(t *testing.T) {
	paths := productSetupPaths(t)
	result, err := New(paths).Initialize(t.Context(), Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	require.NotEmpty(t, result.APIKey)
	require.FileExists(t, paths.ConfigFile)
	require.DirExists(t, paths.BadgerDir)
	require.NoDirExists(t, filepath.Join(paths.ConfigDir, "data"))
	state, err := Inspect(paths)
	require.NoError(t, err)
	require.Equal(t, StateReady, state)
}

func TestInitializeSeparateRootsPreservesExistingData(t *testing.T) {
	paths := productSetupPaths(t)
	require.NoError(t, os.MkdirAll(paths.BadgerDir, 0o700))
	marker := filepath.Join(paths.BadgerDir, "existing-state")
	require.NoError(t, os.WriteFile(marker, []byte("preserve"), 0o600))
	_, err := New(paths).Initialize(t.Context(), Request{APIKeyName: "local-admin"})
	require.ErrorIs(t, err, ErrPartialState)
	require.NoFileExists(t, paths.ConfigFile)
	content, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(content))
}

func TestRollbackSeparateProductRoots(t *testing.T) {
	paths := productSetupPaths(t)
	service := New(paths)
	result, err := service.Initialize(t.Context(), Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
	require.NoError(t, service.Rollback(t.Context(), result))
	require.NoFileExists(t, paths.ConfigFile)
	require.NoDirExists(t, paths.BadgerDir)
	_, err = service.Initialize(t.Context(), Request{APIKeyName: "local-admin"})
	require.NoError(t, err)
}

func TestSetupSelectedLeafPaths(t *testing.T) {
	for _, layout := range []string{"external-leaves", "nested-leaves", "same-parent", "nested-roots"} {
		t.Run(layout, func(t *testing.T) {
			paths := productSetupPaths(t)
			base := t.TempDir()
			switch layout {
			case "external-leaves":
				paths.ConfigFile = filepath.Join(base, "configuration", "selected.env")
				paths.BadgerDir = filepath.Join(base, "database", "selected-kv")
			case "nested-leaves":
				paths.ConfigFile = filepath.Join(paths.ConfigDir, "selected", "primary.env")
				paths.BadgerDir = filepath.Join(paths.DataDir, "selected", "kv")
			case "same-parent":
				paths.ConfigDir, paths.DataDir = filepath.Join(base, "shared"), filepath.Join(base, "shared")
				paths.ConfigFile, paths.BadgerDir = filepath.Join(paths.ConfigDir, "primary.env"), filepath.Join(paths.DataDir, "kv")
			case "nested-roots":
				paths = config.PathsForConfigDir(filepath.Join(base, "private"))
			}
			service := New(paths)
			result, err := service.Initialize(t.Context(), Request{APIKeyName: "local-admin"})
			require.NoError(t, err)
			require.FileExists(t, paths.ConfigFile)
			require.DirExists(t, paths.BadgerDir)
			state, err := Inspect(paths)
			require.NoError(t, err)
			require.Equal(t, StateReady, state)
			require.NoError(t, service.Rollback(t.Context(), result))
			state, err = Inspect(paths)
			require.NoError(t, err)
			require.Equal(t, StateAbsent, state)
		})
	}
}

func TestSetupRejectsOverlappingPathsBeforeWriting(t *testing.T) {
	for _, layout := range []string{"relative", "config-in-database", "database-in-metadata", "config-is-directory", "reserved-name", "config-in-storage-metadata"} {
		t.Run(layout, func(t *testing.T) {
			paths := productSetupPaths(t)
			switch layout {
			case "relative":
				paths.ConfigFile = "relative.env"
			case "config-in-database":
				paths.ConfigFile = filepath.Join(paths.BadgerDir, "primary.env")
			case "database-in-metadata":
				paths.BadgerDir = filepath.Join(paths.ConfigDir, setupMetadataDirectory, "kv")
			case "config-is-directory":
				paths.ConfigFile = paths.ConfigDir
			case "reserved-name":
				paths.BadgerDir = filepath.Join(paths.DataDir, ".record-publications")
			case "config-in-storage-metadata":
				paths.ConfigFile = filepath.Join(filepath.Dir(paths.BadgerDir), databaseLockDirectory(paths), "primary.env")
			}
			_, err := New(paths).Initialize(t.Context(), Request{APIKeyName: "local-admin"})
			require.ErrorIs(t, err, ErrPathsRequired)
			require.NoDirExists(t, paths.ConfigDir)
			require.NoDirExists(t, paths.DataDir)
		})
	}
}

func TestInspectFindsDataWithoutConfigurationRoot(t *testing.T) {
	paths := productSetupPaths(t)
	require.NoError(t, os.MkdirAll(paths.BadgerDir, privateDirMode))
	require.NoDirExists(t, paths.ConfigDir)
	state, err := Inspect(paths)
	require.NoError(t, err)
	require.Equal(t, StatePartial, state)
	require.NoDirExists(t, paths.ConfigDir)
}
