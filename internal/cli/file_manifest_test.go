package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfigFileManifestReportsSelectedStorageWithoutOpeningIt(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "absent"))
	loader := config.NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles()
	deps, stdout, _ := testDependencies()
	deps.LoadConfig = func(ctx context.Context) (*config.Config, error) { return loader.Load(ctx) }
	require.NoError(t, Run(t.Context(), []string{"starport", "config", "paths", "--files", "--json"}, deps))
	var report productpaths.FileManifest
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Equal(t, productpaths.Starport, report.Product)
	entries := make(map[string]productpaths.FileEntry)
	for _, entry := range report.Files {
		entries[entry.ID] = entry
	}
	require.Equal(t, paths.BadgerDir, entries["badger"].Location.Path)
	require.Equal(t, "available", entries["badger"].Availability)
	require.Equal(t, paths.SQLiteFile, entries["sqlite"].Location.Path)
	require.Equal(t, "owner-only", entries["runtime-evidence"].Policy.Access)
	require.Equal(t, "public-read", entries["baseline"].Policy.Access)
	require.Nil(t, report.Inspection)
	require.NoDirExists(t, paths.ConfigDir)
}

func TestConfigFileManifestInspectionHasABoundAndReadsNoContents(t *testing.T) {
	paths := config.PathsForConfigDir(t.TempDir())
	require.NoError(t, os.MkdirAll(paths.BadgerDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(paths.BadgerDir, "MANIFEST"), []byte("private-record-bytes"), 0o600))
	loader := config.NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles()
	deps, stdout, _ := testDependencies()
	deps.LoadConfig = func(ctx context.Context) (*config.Config, error) { return loader.Load(ctx) }
	require.NoError(t, Run(t.Context(), []string{"starport", "config", "paths", "--inspect", "--max-entries", "1", "--json"}, deps))
	var report productpaths.FileManifest
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.NotNil(t, report.Inspection)
	require.False(t, report.Inspection.Complete)
	require.Equal(t, 1, report.Inspection.Examined)
	require.NotContains(t, stdout.String(), "private-record-bytes")
	require.NoFileExists(t, filepath.Join(paths.BadgerDir, "LOCK"))
}

func TestConfigFileManifestRejectsInvalidInspectionBeforeLoading(t *testing.T) {
	for _, args := range [][]string{
		{"--max-entries", "1"}, {"--inspect", "--max-entries", "0"}, {"--inspect", "--max-entries", "100001"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			deps, _, _ := testDependencies()
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid inspection must not load configuration")
				return nil, nil
			}
			err := Run(t.Context(), append([]string{"starport", "config", "paths"}, args...), deps)
			require.Error(t, err)
		})
	}
}

func TestConfigFileManifestReportsPrivateStateAccessConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode conflict. Native DACL checks belong to the shared inspector.")
	}
	paths := config.PathsForConfigDir(t.TempDir())
	require.NoError(t, os.MkdirAll(paths.RuntimeDir, 0o700))
	require.NoError(t, os.Chmod(paths.RuntimeDir, 0o755))
	loader := config.NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles()
	deps, stdout, _ := testDependencies()
	deps.LoadConfig = func(ctx context.Context) (*config.Config, error) { return loader.Load(ctx) }
	require.NoError(t, Run(t.Context(), []string{"starport", "config", "paths", "--inspect", "--json"}, deps))
	var report productpaths.FileManifest
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	for _, observed := range report.Inspection.Observations {
		if observed.ID == "runtime-evidence" && observed.Path == paths.RuntimeDir {
			require.Equal(t, "owner-only", observed.AccessPolicy)
			require.Equal(t, "conflict", observed.AccessStatus)
			return
		}
	}
	t.Fatal("runtime access observation is absent")
}

func TestConfigFileManifestInspectionPreservesCancellation(t *testing.T) {
	loader := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvironment(nil).WithEnvFiles()
	cfg, err := loader.Load(t.Context())
	require.NoError(t, err)
	deps, _, _ := testDependencies()
	deps.LoadConfig = func(context.Context) (*config.Config, error) { return cfg, nil }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = Run(ctx, []string{"starport", "config", "paths", "--inspect"}, deps)
	require.ErrorIs(t, err, context.Canceled)
}
