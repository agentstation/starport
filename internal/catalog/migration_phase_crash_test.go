package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/stretchr/testify/require"
)

func TestMigrationPhaseProcessRecovery(t *testing.T) {
	if root := os.Getenv("STARPORT_TEST_MIGRATION_PHASE_ROOT"); root != "" {
		migrationPhaseChild(t, root, os.Getenv("STARPORT_TEST_MIGRATION_PHASE_STOP"))
		return
	}
	// Each child decodes the compiled baseline under race instrumentation.
	// Serialize cold processes while fixture setup and recovery stay parallel.
	var childProcess sync.Mutex
	for _, phase := range []string{"stage", "publish", "complete"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			// Prepare durable input before the bounded crash process.
			// The child owns every migration operation.
			prepareMigrationPhase(t, root)
			executable, err := os.Executable()
			require.NoError(t, err)
			childProcess.Lock()
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			child := exec.CommandContext(ctx, executable, "-test.run=^TestMigrationPhaseProcessRecovery$")
			child.Env = append(os.Environ(), "STARPORT_TEST_MIGRATION_PHASE_ROOT="+root, "STARPORT_TEST_MIGRATION_PHASE_STOP="+phase)
			output, err := child.CombinedOutput()
			cancel()
			childProcess.Unlock()
			var exited *exec.ExitError
			require.ErrorAs(t, err, &exited, string(output))
			require.Equal(t, migrationCrashExit, exited.ExitCode(), string(output))
			raw, err := os.ReadFile(filepath.Join(root, "request.json"))
			require.NoError(t, err)
			var migration RuntimeMigration
			require.NoError(t, json.Unmarshal(raw, &migration))
			store := authoritySnapshotBadger(t, filepath.Join(root, "badger"))
			settings := migrationPhaseSettings(root)
			generations, err := NewGenerationStore(store)
			require.NoError(t, err)
			before, err := generations.Current(t.Context())
			require.NoError(t, err)
			if phase == "stage" {
				require.NoDirExists(t, migration.TargetDirectory)
				_, err = migration.Stage(t.Context(), store, settings)
				require.NoError(t, err)
			}
			if phase != "complete" {
				_, err = migration.Publish(t.Context(), store, settings)
				require.NoError(t, err)
			}
			require.DirExists(t, migration.SourceDirectory, "source data must survive process recovery")
			raw, err = os.ReadFile(filepath.Join(root, "source-checksums.json"))
			require.NoError(t, err)
			var preserved map[string][32]byte
			require.NoError(t, json.Unmarshal(raw, &preserved))
			require.NotEmpty(t, preserved)
			for relative, expected := range preserved {
				data, err := os.ReadFile(filepath.Join(migration.SourceDirectory, relative))
				require.NoError(t, err, relative)
				require.Equal(t, expected, sha256.Sum256(data), relative)
			}
			settings.StateDirectory = migration.TargetDirectory
			settings.Values[catalogconfig.SchedulerIdentity] = migration.SourceIdentity
			replacement, err := migration.OpenReplacement(t.Context(), store, settings)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, replacement.Close(context.Background())) })
			completed, err := migration.Complete(t.Context(), replacement, settings)
			require.NoError(t, err)
			require.Equal(t, "completed", completed.Phase)
			require.Equal(t, migration.SourceIdentity, replacement.Status().InstanceIdentity)
			require.Equal(t, before.Manifest.GenerationID, replacement.ControlPlane().Current().GenerationID())
			after, err := generations.Current(t.Context())
			require.NoError(t, err)
			require.Equal(t, before.Payload, after.Payload)
		})
	}
}

func migrationPhaseSettings(root string) Settings {
	settings := identityTestSettings(filepath.Join(root, "source"), "", "")
	settings.Values = map[string]string{}
	settings.Source = "file"
	settings.SourceURL = filepath.Join(root, "catalog.json")
	settings.SourceStartupPolicy = "require_source"
	settings.SourcePollInterval = 0
	return settings
}

func prepareMigrationPhase(t *testing.T, root string) {
	t.Helper()
	payload, err := catalogs.EncodeCatalogPayload(acquisitionLifecycleCatalog(t, "https://provider.invalid"))
	require.NoError(t, err)
	settings := migrationPhaseSettings(root)
	require.NoError(t, os.WriteFile(settings.SourceURL, payload, 0600))
	store := authoritySnapshotBadger(t, filepath.Join(root, "badger"))
	original, err := OpenRuntime(t.Context(), store, settings, nil)
	require.NoError(t, err)
	candidate, err := original.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, original.Accept(t.Context(), candidate))
	migration := RuntimeMigration{OperationID: "phase-recovery", SourceDirectory: settings.StateDirectory, TargetDirectory: filepath.Join(root, "target"), JournalRoot: filepath.Join(root, "journal"), SourceIdentity: original.Status().InstanceIdentity}
	require.NoError(t, original.Close(t.Context()))
	preserved := map[string][32]byte{}
	require.NoError(t, filepath.WalkDir(migration.SourceDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(migration.SourceDirectory, path)
		if err != nil {
			return err
		}
		preserved[relative] = sha256.Sum256(data)
		return nil
	}))
	checksums, err := json.Marshal(preserved)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "source-checksums.json"), checksums, 0600))
	raw, err := json.Marshal(migration)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "request.json"), raw, 0600))
	require.NoError(t, store.Close())
}

func migrationPhaseChild(t *testing.T, root, stop string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "request.json"))
	require.NoError(t, err)
	var migration RuntimeMigration
	require.NoError(t, json.Unmarshal(raw, &migration))
	settings := migrationPhaseSettings(root)
	store := authoritySnapshotBadger(t, filepath.Join(root, "badger"))
	_, err = migration.Prepare(t.Context(), store, settings)
	require.NoError(t, err)
	_, err = migration.Stage(t.Context(), store, settings)
	require.NoError(t, err)
	if stop == "stage" {
		os.Exit(migrationCrashExit)
	}
	_, err = migration.Publish(t.Context(), store, settings)
	require.NoError(t, err)
	if stop == "publish" {
		os.Exit(migrationCrashExit)
	}
	settings.StateDirectory = migration.TargetDirectory
	settings.Values[catalogconfig.SchedulerIdentity] = migration.SourceIdentity
	replacement, err := migration.OpenReplacement(t.Context(), store, settings)
	require.NoError(t, err)
	_, err = migration.Complete(t.Context(), replacement, settings)
	require.NoError(t, err)
	if stop == "complete" {
		os.Exit(migrationCrashExit)
	}
	t.Fatal("unknown process stop phase")
}
