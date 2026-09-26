package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRuntimeDirectoryMigrationPreservesAcceptedCatalog(t *testing.T) {
	root := t.TempDir()
	settings := identityTestSettings(filepath.Join(root, "source"), "", "")
	settings.Values = map[string]string{}
	payload, err := catalogs.EncodeCatalogPayload(acquisitionLifecycleCatalog(t, "https://provider.invalid"))
	require.NoError(t, err)
	settings.Source = string(runtime.SourceFile)
	settings.SourceURL = filepath.Join(root, "catalog.json")
	settings.SourceStartupPolicy = string(runtime.StartupRequireSource)
	settings.SourcePollInterval = 0
	require.NoError(t, os.WriteFile(settings.SourceURL, payload, 0600))
	store := authoritySnapshotBadger(t, filepath.Join(root, "badger"))
	original, err := OpenRuntime(t.Context(), store, settings, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, original.Close(context.Background())) })
	candidate, err := original.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, original.Accept(t.Context(), candidate))
	accepted, err := original.accepted.Get(t.Context(), candidate.State.GenerationID)
	require.NoError(t, err)
	migration := RuntimeMigration{OperationID: "move-runtime", SourceDirectory: settings.StateDirectory, TargetDirectory: filepath.Join(root, "target"), JournalRoot: filepath.Join(root, "journal"), SourceIdentity: original.runtime.Status().InstanceIdentity}
	_, err = migration.Prepare(t.Context(), store, settings)
	require.Error(t, err, "an active source must retain its directory lock")
	require.NoError(t, original.Close(t.Context()))
	prepared, err := migration.Prepare(t.Context(), store, settings)
	require.NoError(t, err)
	require.NotNil(t, prepared.IdentityVerified)
	require.True(t, *prepared.IdentityVerified)
	require.FileExists(t, filepath.Join(prepared.HostJournalDirectory, "catalog-binding.json"))
	require.Positive(t, prepared.FileCount)
	require.NoDirExists(t, migration.TargetDirectory)
	retryStore := storage.NewMockStore()
	retryAccepted, err := NewGenerationStore(retryStore)
	require.NoError(t, err)
	require.NoError(t, retryAccepted.Commit(t.Context(), accepted, ""))
	_, err = migration.Prepare(t.Context(), retryStore, settings)
	require.Error(t, err, "the prepared host journal must reject a different store")
	staged, err := migration.Stage(t.Context(), store, settings)
	require.NoError(t, err)
	require.Equal(t, prepared.FileCount, staged.FileCount)
	require.NotNil(t, staged.IdentityVerified)
	require.True(t, *staged.IdentityVerified)
	require.NoDirExists(t, migration.TargetDirectory)
	published, err := migration.Publish(t.Context(), store, settings)
	require.NoError(t, err)
	require.Equal(t, migration.SourceIdentity, published.SchedulerIdentity)
	require.DirExists(t, migration.SourceDirectory)
	_, err = OpenRuntime(t.Context(), store, settings, nil)
	require.Error(t, err, "a retired source must not restart")
	_, err = migration.OpenReplacement(t.Context(), store, settings)
	require.Error(t, err, "the host must select the target and retained identity")
	settings.StateDirectory = migration.TargetDirectory
	settings.Values[catalogconfig.SchedulerIdentity] = migration.SourceIdentity
	foreign, foreignErr := migration.OpenReplacement(t.Context(), storage.NewMockStore(), settings)
	if foreign != nil {
		require.NoError(t, foreign.Close(t.Context()))
	}
	require.Error(t, foreignErr, "migration must not bootstrap an empty replacement catalog store")
	foreignStore := storage.NewMockStore()
	foreignAccepted, err := NewGenerationStore(foreignStore)
	require.NoError(t, err)
	require.NoError(t, foreignAccepted.Commit(t.Context(), accepted, ""))
	_, err = migration.OpenReplacement(t.Context(), foreignStore, settings)
	require.Error(t, err, "a different nonempty store has no original operation binding")
	replacement, err := migration.OpenReplacement(t.Context(), store, settings)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, replacement.Close(context.Background())) })
	require.Equal(t, candidate.State.GenerationID, replacement.ControlPlane().Current().GenerationID())
	retained, err := replacement.accepted.Get(t.Context(), candidate.State.GenerationID)
	require.NoError(t, err)
	require.Equal(t, accepted.Payload, retained.Payload)
	completed, err := migration.Complete(t.Context(), replacement, settings)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Phase)
	require.Equal(t, prepared.HostJournalDirectory, completed.HostJournalDirectory)
	require.NoError(t, replacement.Close(t.Context()))
	require.NoError(t, store.Close())
	store = authoritySnapshotBadger(t, filepath.Join(root, "badger"))
	reopened, err := OpenRuntime(t.Context(), store, settings, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close(context.Background())) })
	require.Equal(t, candidate.State.GenerationID, reopened.ControlPlane().Current().GenerationID())
	require.Equal(t, migration.SourceIdentity, reopened.runtime.Status().InstanceIdentity)
}

func TestRuntimeMigrationRefusesUnpublishedTargetBeforeStorage(t *testing.T) {
	root := t.TempDir()
	settings := identityTestSettings(filepath.Join(root, "target"), "", "")
	settings.Values = map[string]string{catalogconfig.SchedulerIdentity: "original"}
	settings.BaselineDirectory = filepath.Join(root, "baseline")
	migration := RuntimeMigration{OperationID: "unpublished", SourceDirectory: filepath.Join(root, "source"), TargetDirectory: settings.StateDirectory, JournalRoot: filepath.Join(root, "journal"), SourceIdentity: "original"}
	_, err := migration.OpenReplacement(t.Context(), storage.NewMockStore(), settings)
	require.Error(t, err)
	require.NoDirExists(t, settings.StateDirectory)
	require.NoDirExists(t, settings.BaselineDirectory)
	require.NoDirExists(t, migration.JournalRoot)
}
