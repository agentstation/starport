package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func baselinePublicationFixture(t *testing.T, mode string) (*config.Config, recovery.PublishFilesRequest, string) {
	t.Helper()
	source, capture := backupApplicationFixture(t)
	paths := source.EffectivePaths()
	exported, err := starmap.ExportEmbeddedBaseline(t.Context(), paths.BaselineDir)
	require.NoError(t, err)
	switch mode {
	case "journal", "changed-stage":
		baselinePublicationStageFixture(t, paths.BaselineDir, mode)
	case "corrupt":
		require.NoError(t, os.WriteFile(filepath.Join(exported.Directory, "catalog.json"), []byte("invalid captured catalog"), 0600))
	case "stage":
		stage := filepath.Join(paths.BaselineDir, ".baseline-unfinished")
		_, err := productfiles.CreateDirectory(stage)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(stage, "manifest.json"), []byte("unfinished"), 0600))
	}
	_, err = CloseBackupBoundary(t.Context(), source)
	require.NoError(t, err)
	receipt, err := CaptureBackup(t.Context(), source, capture)
	require.NoError(t, err)
	parent := filepath.Join(t.TempDir(), "target")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	target, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_SECURITY_MASTER_KEY": source.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": paths.DeploymentID, "STARPORT_INSTANCE_ID": paths.InstanceID,
	}).Load(t.Context())
	require.NoError(t, err)
	return target, recovery.PublishFilesRequest{PrepareRequest: recovery.PrepareRequest{
		VerifyRequest:  recovery.VerifyRequest{Directory: receipt.Directory, ManifestSHA256: receipt.ManifestSHA256},
		FilesDirectory: filepath.Join(parent, "prepared"), Operation: recovery.RestoreOperation{ID: "restore-baseline", FencingEvidence: "incident/fenced-writers"},
	}, Role: config.BaselineRole}, exported.GenerationID
}

func TestRestorePublishBaselinePreservesExportAndLeavesJournalsInactive(t *testing.T) {
	cfg, request, generation := baselinePublicationFixture(t, "valid")
	first, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, first.Tree.Published)
	require.Equal(t, cfg.EffectivePaths().BaselineDir, first.Tree.Destination)
	require.Equal(t, 2, first.Tree.Files)
	require.NoDirExists(t, filepath.Join(first.Tree.Destination, ".starmap-baseline"))
	require.NotEmpty(t, first.Remaining)
	foundJournal := false
	for _, file := range first.Remaining {
		require.NotEqual(t, config.BaselineRole, file.Role)
		foundJournal = foundJournal || file.Role == "baseline-recovery"
	}
	require.True(t, foundJournal, "captured journals must remain inactive")
	requirePublicationBarriers(t, cfg)
	again, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, again.Tree.Reused)
	require.Equal(t, first.Tree.DirectoryIdentity, again.Tree.DirectoryIdentity)
	requirePublicationBarriers(t, cfg)
	// The installed binary can verify its export and create fresh journal ownership.
	current, err := starmap.ExportEmbeddedBaseline(t.Context(), cfg.EffectivePaths().BaselineDir)
	require.NoError(t, err)
	require.False(t, current.Created)
	require.Equal(t, generation, current.GenerationID)
	require.Empty(t, current.Recovery.PreservedPaths)
	requirePublicationBarriers(t, cfg)
}

func TestRestorePublishBaselineRefusesInvalidExportsAndStages(t *testing.T) {
	for _, mode := range []string{"corrupt", "stage"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request, _ := baselinePublicationFixture(t, mode)
			_, err := PublishBackupFiles(t.Context(), cfg, request)
			require.Error(t, err)
			require.NoDirExists(t, cfg.EffectivePaths().BaselineDir)
			if mode == "stage" {
				require.NoDirExists(t, cfg.EffectivePaths().BadgerDir)
				require.NoFileExists(t, cfg.EffectivePaths().SQLiteFile)
			} else {
				requirePublicationBarriers(t, cfg)
			}
		})
	}
}

func TestRestorePublishBaselinePreservesExistingConflictingTarget(t *testing.T) {
	cfg, request, _ := baselinePublicationFixture(t, "valid")
	target := cfg.EffectivePaths().BaselineDir
	_, err := productfiles.NewDirectory(target)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(target, "operator-file"), []byte("preserve"), 0600))
	result, err := PublishBackupFiles(t.Context(), cfg, request)
	require.Error(t, err)
	require.False(t, result.Tree.Published)
	body, err := os.ReadFile(filepath.Join(target, "operator-file"))
	require.NoError(t, err)
	require.Equal(t, "preserve", string(body))
	requirePublicationBarriers(t, cfg)
}

func baselinePublicationStageFixture(t *testing.T, directory, mode string) {
	t.Helper()
	token := strings.Repeat("A", 26)
	stage := filepath.Join(directory, ".baseline-"+token)
	_, err := productfiles.CreateDirectory(stage)
	require.NoError(t, err)
	body := []byte("retained candidate; never a completed export")
	journal, err := os.ReadFile("testdata/baseline-publication.json")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, starmap.BaselineRecoveryDirectoryName, token+".json"), journal, 0600))
	if mode == "changed-stage" {
		body = append(body, '!')
	}
	require.NoError(t, os.WriteFile(filepath.Join(stage, "catalog.json"), body, 0600))
}

func TestRestorePublishBaselineRetainsVerifiedStageWithoutPromotion(t *testing.T) {
	cfg, request, _ := baselinePublicationFixture(t, "journal")
	result, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, result.Tree.Published)
	require.Equal(t, 2, result.Tree.Files)
	stage := ".baseline-" + strings.Repeat("A", 26)
	require.NoDirExists(t, filepath.Join(result.Tree.Destination, stage))
	var selected bool
	for _, file := range result.Remaining {
		if file.Role == config.BaselineRecoveryRole {
			require.Equal(t, "verified-history", file.Action)
			require.Empty(t, file.Destination)
		}
		if file.Role == config.BaselineRole {
			require.Equal(t, "verified-staging", file.Action)
			require.Empty(t, file.Destination)
			original, err := os.ReadFile(filepath.Join(request.Directory, "files", filepath.FromSlash(file.ArtifactID)))
			require.NoError(t, err)
			prepared, err := os.ReadFile(filepath.Join(request.FilesDirectory, "files", filepath.FromSlash(file.ArtifactID)))
			require.NoError(t, err)
			require.Equal(t, original, prepared)
			selected = true
		}
	}
	require.True(t, selected)
	requirePublicationBarriers(t, cfg)
	again, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, again.Tree.Reused)
	require.Equal(t, result.Remaining, again.Remaining)
	requirePublicationBarriers(t, cfg)
}

func TestRestorePublishBaselineRefusesChangedStageBeforePreparation(t *testing.T) {
	cfg, request, _ := baselinePublicationFixture(t, "changed-stage")
	_, err := PublishBackupFiles(t.Context(), cfg, request)
	require.Error(t, err)
	require.NoDirExists(t, cfg.EffectivePaths().BaselineDir)
	require.NoDirExists(t, cfg.EffectivePaths().BadgerDir)
	require.NoFileExists(t, cfg.EffectivePaths().SQLiteFile)
}
