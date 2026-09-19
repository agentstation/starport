package app

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/storage"
	"github.com/joho/godotenv"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRuntimeMigrationRefusesBeforeStorageCreation(t *testing.T) {
	for _, phase := range []string{"prepare", "stage", "publish", "complete", "unknown"} {
		t.Run(phase, func(t *testing.T) {
			home := t.TempDir()
			cfg, err := config.NewLoader().WithEnvironment(map[string]string{"STARPORT_HOME": home}).Load(t.Context())
			require.NoError(t, err)
			paths := cfg.EffectivePaths()
			migration := catalog.RuntimeMigration{OperationID: "move", SourceDirectory: paths.RuntimeDir, TargetDirectory: filepath.Join(home, "target"), JournalRoot: filepath.Join(home, "journal"), SourceIdentity: "original"}
			_, err = MigrateRuntime(t.Context(), cfg, phase, migration)
			require.Error(t, err)
			require.NoDirExists(t, paths.BadgerDir)
			require.NoDirExists(t, migration.JournalRoot)
			require.NoDirExists(t, migration.TargetDirectory)
		})
	}
}

func TestRuntimeMigrationOperatorLifecycle(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "config.env")
	source := filepath.Join(home, "source")
	payload, err := catalogs.EncodeCatalogPayload(syntheticInferenceCatalog(t, "https://provider.invalid"))
	require.NoError(t, err)
	baseline := filepath.Join(home, "catalog.json")
	require.NoError(t, os.WriteFile(baseline, payload, 0600))
	values := map[string]string{
		"STARPORT_CATALOG_STATE_DIR":             source,
		"STARPORT_CATALOG_SOURCE":                "file",
		"STARPORT_CATALOG_SOURCE_URL":            baseline,
		"STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "require_source",
		"STARPORT_CATALOG_SOURCE_POLL_INTERVAL":  "0s",
		"STARPORT_CATALOG_ACQUISITION_ENABLED":   "false",
		"STARPORT_CATALOG_STARTUP_SPREAD":        "0s",
	}
	load := func() *config.Config {
		t.Helper()
		contents, err := godotenv.Marshal(values)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(file, []byte(contents+"\n"), 0600))
		cfg, err := config.NewLoader().WithEnvironment(map[string]string{"STARPORT_HOME": home, "STARPORT_CONFIG_FILE": file}).Load(t.Context())
		require.NoError(t, err)
		return cfg
	}
	cfg := load()
	require.NoError(t, os.MkdirAll(cfg.EffectivePaths().BadgerDir, 0700))
	store, err := storage.Open(cfg.Storage.RuntimeStorage())
	require.NoError(t, err)
	original, err := catalog.OpenRuntime(t.Context(), store, catalogSettings(cfg), nil)
	require.NoError(t, err)
	candidate, err := original.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, original.Accept(t.Context(), candidate))
	identity := original.Status().InstanceIdentity
	require.NoError(t, original.Close(t.Context()))
	require.NoError(t, store.Close())
	migration := catalog.RuntimeMigration{OperationID: "operator-move", SourceDirectory: source, TargetDirectory: filepath.Join(home, "target"), JournalRoot: filepath.Join(home, "journal"), SourceIdentity: identity}
	for _, phase := range []string{"prepare", "stage", "publish"} {
		_, err := MigrateRuntime(t.Context(), cfg, phase, migration)
		require.NoError(t, err, phase)
	}
	_, err = MigrateRuntime(t.Context(), cfg, "complete", migration)
	require.Error(t, err, "completion must require the saved target selection")
	values["STARPORT_CATALOG_STATE_DIR"] = migration.TargetDirectory
	values["STARPORT_SCHEDULER_IDENTITY"] = identity
	cfg = load()
	completed, err := MigrateRuntime(t.Context(), cfg, "complete", migration)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Phase)
	require.Equal(t, identity, completed.SchedulerIdentity)
	require.NoFileExists(t, cfg.EffectivePaths().SQLiteFile, "migration must not open SQL")
	store, err = storage.Open(cfg.Storage.RuntimeStorage())
	require.NoError(t, err)
	defer store.Close()
	generations, err := catalog.NewGenerationStore(store)
	require.NoError(t, err)
	retained, err := generations.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, candidate.State.GenerationID, retained.Manifest.GenerationID)
}
