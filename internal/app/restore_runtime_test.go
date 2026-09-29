package app

import (
	"os"
	"path/filepath"
	"testing"

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
	source, err := config.NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	store, err := storage.Open(source.RuntimeStorage())
	require.NoError(t, err)
	runtime, err := catalog.OpenRuntime(t.Context(), store, catalogSettings(source), nil)
	require.NoError(t, err)
	require.NotEmpty(t, runtime.Status().InstanceIdentity)
	require.NoError(t, runtime.Close(t.Context()))
	require.NoError(t, store.Close())
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
