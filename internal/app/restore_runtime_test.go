package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func runtimePublicationFixture(t *testing.T, mode string) (*config.Config, recovery.PublishFilesRequest, []byte) {
	t.Helper()
	source, capture := backupApplicationFixture(t)
	paths := source.EffectivePaths()
	values := map[string]string{
		"STARPORT_SECURITY_MASTER_KEY": source.Security.MasterKey,
		"STARPORT_DEPLOYMENT_ID":       paths.DeploymentID, "STARPORT_INSTANCE_ID": paths.InstanceID,
		"STARPORT_CATALOG_SOURCE": "embedded", "STARPORT_CATALOG_SOURCE_POLL_INTERVAL": "0s",
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false", "STARPORT_CATALOG_STARTUP_SPREAD": "0s",
	}
	migrated := mode == "completed-migration" || mode == "incomplete-migration" || mode == "mismatched-migration" || mode == "pending-migration"
	if migrated {
		payload, err := catalogs.EncodeCatalogPayload(syntheticInferenceCatalog(t, "https://provider.invalid"))
		require.NoError(t, err)
		fixture := filepath.Join(paths.ConfigDir, "migration-source.json")
		require.NoError(t, os.WriteFile(fixture, payload, 0600))
		values["STARPORT_CATALOG_SOURCE"] = "file"
		values["STARPORT_CATALOG_SOURCE_URL"] = fixture
		values["STARPORT_CATALOG_SOURCE_STARTUP_POLICY"] = "require_source"
	}
	source, err := config.NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	store, err := storage.Open(source.RuntimeStorage())
	require.NoError(t, err)
	runtime, err := catalog.OpenRuntime(t.Context(), store, catalogSettings(source), nil)
	require.NoError(t, err)
	identity := runtime.Status().InstanceIdentity
	require.NotEmpty(t, identity)
	if migrated {
		candidate, err := runtime.CurrentCandidate(t.Context())
		require.NoError(t, err)
		require.NoError(t, runtime.Accept(t.Context(), candidate))
	}
	require.NoError(t, runtime.Close(t.Context()))
	require.NoError(t, store.Close())
	if migrated {
		source = migrateRuntimePublicationFixture(t, source, values, identity)
		paths = source.EffectivePaths()
		if mode == "incomplete-migration" {
			require.NoError(t, os.Remove(filepath.Join(paths.RuntimeDir, ".migration-completed.json")))
		}
		if mode == "mismatched-migration" {
			require.NoError(t, os.WriteFile(filepath.Join(paths.RuntimeDir, ".migration-completed.json"), []byte("conflicting history"), 0600))
		}
		if mode == "pending-migration" {
			record, err := os.ReadFile(filepath.Join(paths.RuntimeDir, ".migration-completed.json"))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(paths.RuntimeDir, ".migration-pending.json"), record, 0600))
		}
	}
	seed, err := os.ReadFile(filepath.Join(paths.RuntimeDir, "instance-seed"))
	require.NoError(t, err)
	if mode == "corrupt" {
		require.NoError(t, os.WriteFile(filepath.Join(paths.RuntimeDir, "instance-seed"), []byte("invalid"), 0600))
	}
	_, err = CloseBackupBoundary(t.Context(), source)
	require.NoError(t, err)
	receipt, err := CaptureBackup(t.Context(), source, capture)
	require.NoError(t, err)
	parent := filepath.Join(t.TempDir(), "target")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	if mode == "different-replica" {
		values["STARPORT_INSTANCE_ID"] = "another-replica"
	}
	if mode == "different-override" {
		values["STARPORT_SCHEDULER_IDENTITY"] = "different-identity"
	}
	delete(values, "STARPORT_CATALOG_STATE_DIR")
	target, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	return target, recovery.PublishFilesRequest{PrepareRequest: recovery.PrepareRequest{
		VerifyRequest:  recovery.VerifyRequest{Directory: receipt.Directory, ManifestSHA256: receipt.ManifestSHA256},
		FilesDirectory: filepath.Join(parent, "prepared"), Operation: recovery.RestoreOperation{ID: "restore-runtime", FencingEvidence: "incident/fenced-writers"},
	}, Role: config.RuntimeEvidenceRole}, seed
}

func TestRestorePublishRuntimeRetainsIdentityWithoutAdmission(t *testing.T) {
	cfg, request, seed := runtimePublicationFixture(t, "valid")
	first, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, first.Tree.Published)
	require.Equal(t, cfg.EffectivePaths().RuntimeDir, first.Tree.Destination)
	require.NoError(t, catalogSettings(cfg).InspectRetainedDirectory(t.Context(), first.Tree.Destination))
	body, err := os.ReadFile(filepath.Join(first.Tree.Destination, "instance-seed"))
	require.NoError(t, err)
	require.Equal(t, seed, body)
	require.NotEmpty(t, first.Remaining)
	for _, file := range first.Remaining {
		require.NotEqual(t, config.RuntimeEvidenceRole, file.Role)
	}
	requirePublicationBarriers(t, cfg)
	again, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, again.Tree.Reused)
	require.Equal(t, first.Tree.DirectoryIdentity, again.Tree.DirectoryIdentity)
	requirePublicationBarriers(t, cfg)
}

func TestRestorePublishRuntimeRefusesInvalidIdentity(t *testing.T) {
	for _, mode := range []string{"corrupt", "different-replica", "different-override"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request, _ := runtimePublicationFixture(t, mode)
			_, err := PublishBackupFiles(t.Context(), cfg, request)
			require.Error(t, err)
			require.NoDirExists(t, cfg.EffectivePaths().RuntimeDir)
			if mode != "different-replica" {
				requirePublicationBarriers(t, cfg)
			} else {
				require.NoDirExists(t, cfg.EffectivePaths().BadgerDir)
			}
		})
	}
}

func migrateRuntimePublicationFixture(t *testing.T, source *config.Config, values map[string]string, identity string) *config.Config {
	t.Helper()
	paths := source.EffectivePaths()
	store, err := storage.Open(source.RuntimeStorage())
	require.NoError(t, err)
	selection, err := source.Storage.MigrationStorageSelection()
	require.NoError(t, err)
	migration := catalog.RuntimeMigration{OperationID: "before-backup", SourceDirectory: paths.RuntimeDir, TargetDirectory: filepath.Join(paths.StateDir, "moved-runtime"), JournalRoot: filepath.Join(paths.StateDir, "migration-history"), SourceIdentity: identity, StoreSelection: selection}
	settings := catalogSettings(source)
	_, err = migration.Prepare(t.Context(), store, settings)
	require.NoError(t, err)
	_, err = migration.Stage(t.Context(), store, settings)
	require.NoError(t, err)
	_, err = migration.Publish(t.Context(), store, settings)
	require.NoError(t, err)
	values["STARPORT_CATALOG_STATE_DIR"] = migration.TargetDirectory
	values["STARPORT_SCHEDULER_IDENTITY"] = identity
	moved, err := config.NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	replacement, err := migration.OpenReplacement(t.Context(), store, catalogSettings(moved))
	require.NoError(t, err)
	_, err = migration.Complete(t.Context(), replacement, catalogSettings(moved))
	require.NoError(t, err)
	require.NoError(t, replacement.Close(t.Context()))
	require.NoError(t, store.Close())
	return moved
}

func TestRestorePublishRuntimeKeepsCompletedMigrationHistoryInactive(t *testing.T) {
	cfg, request, seed := runtimePublicationFixture(t, "completed-migration")
	first, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, first.Tree.Published)
	require.NoError(t, catalogSettings(cfg).InspectRetainedDirectory(t.Context(), first.Tree.Destination))
	var historical int
	for _, file := range first.Remaining {
		if file.Role != config.RuntimeEvidenceRole {
			continue
		}
		historical++
		require.Equal(t, "verified-history", file.Action)
		require.Empty(t, file.Destination)
		require.NoFileExists(t, filepath.Join(first.Tree.Destination, file.Relative))
		original, err := os.ReadFile(filepath.Join(request.Directory, "files", filepath.FromSlash(file.ArtifactID)))
		require.NoError(t, err)
		require.NotEmpty(t, original)
	}
	require.Equal(t, 2, historical)
	restored, err := os.ReadFile(filepath.Join(first.Tree.Destination, "instance-seed"))
	require.NoError(t, err)
	require.Equal(t, seed, restored)
	requirePublicationBarriers(t, cfg)
	again, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, again.Tree.Reused)
	require.Equal(t, first.Remaining, again.Remaining)
	require.Equal(t, first.Tree.DirectoryIdentity, again.Tree.DirectoryIdentity)
	requirePublicationBarriers(t, cfg)
}

func TestRestorePublishRuntimeRefusesIncompleteMigrationHistoryBeforePreparation(t *testing.T) {
	for _, mode := range []string{"incomplete-migration", "mismatched-migration"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request, _ := runtimePublicationFixture(t, mode)
			_, err := PublishBackupFiles(t.Context(), cfg, request)
			require.Error(t, err)
			require.NoDirExists(t, cfg.EffectivePaths().RuntimeDir)
			require.NoDirExists(t, cfg.EffectivePaths().BadgerDir)
			require.NoFileExists(t, cfg.EffectivePaths().SQLiteFile)
		})
	}
}

func TestRestorePublishRuntimeCannotHidePendingMoveBehindCompletedHistory(t *testing.T) {
	cfg, request, _ := runtimePublicationFixture(t, "pending-migration")
	_, err := PublishBackupFiles(t.Context(), cfg, request)
	require.Error(t, err)
	require.NoDirExists(t, cfg.EffectivePaths().RuntimeDir)
	requirePublicationBarriers(t, cfg)
}
