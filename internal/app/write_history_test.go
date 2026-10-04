package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestWriteImportedHistoryBindsApplication(t *testing.T) {
	cfg, inspection, _ := importedInspectionFixture(t)
	checked, err := InspectImportedBackup(t.Context(), cfg, inspection)
	require.NoError(t, err)
	prepare := recovery.PrepareRequest{VerifyRequest: inspection.VerifyRequest, Operation: inspection.Operation, FilesDirectory: filepath.Join(filepath.Dir(inspection.Destination), "prepared")}
	// The closed inspection receipt names the target that the package must bind.
	request := activationHistoryWith(t, cfg, prepare, func(history recovery.WriteHistoryRequest) recovery.HistoryWriteReport {
		t.Helper()
		history.ExpectedTargetSHA256 = checked.TargetSHA256
		report, err := WriteImportedHistory(t.Context(), cfg, history)
		require.NoError(t, err)
		return report
	})
	require.Equal(t, checked.TargetSHA256, request.History.ExpectedTargetSHA256)
	assertInspectionStillClosed(t, cfg)
	applied, err := ApplyImportedHistory(t.Context(), cfg, request.History)
	require.NoError(t, err)
	require.Equal(t, request.History.HistorySHA256, applied.HistorySHA256)
	require.Equal(t, checked.TargetSHA256, applied.TargetSHA256)
	require.Equal(t, 2, applied.DeclaredSteps)
	require.Equal(t, 2, applied.CompletedSteps)
	require.True(t, applied.KVRotated)
	require.True(t, applied.SQLRotated)
	again, err := ApplyImportedHistory(t.Context(), cfg, request.History)
	require.NoError(t, err)
	require.Equal(t, applied, again)
	assertInspectionStillClosed(t, cfg)
}

func TestWriteImportedHistoryRefusesAnotherDeployment(t *testing.T) {
	cfg, inspection, _ := importedInspectionFixture(t)
	parent := filepath.Dir(inspection.Destination)
	// The same target paths under another deployment identity must not bind this backup.
	foreign, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_SECURITY_MASTER_KEY": cfg.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": "another-deployment",
	}).Load(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, cfg.EffectivePaths().DeploymentID, foreign.EffectivePaths().DeploymentID)
	prepare := recovery.PrepareRequest{VerifyRequest: inspection.VerifyRequest, Operation: inspection.Operation, FilesDirectory: filepath.Join(parent, "prepared")}
	activationHistoryWith(t, cfg, prepare, func(history recovery.WriteHistoryRequest) recovery.HistoryWriteReport {
		t.Helper()
		refused, err := WriteImportedHistory(t.Context(), foreign, history)
		require.ErrorIs(t, err, recovery.ErrConflict)
		require.Zero(t, refused)
		entries, err := os.ReadDir(history.History.Directory)
		require.NoError(t, err)
		require.Empty(t, entries)
		// The refusal leaves the directory usable for the configured deployment.
		report, err := WriteImportedHistory(t.Context(), cfg, history)
		require.NoError(t, err)
		return report
	})
	assertInspectionStillClosed(t, cfg)
}

func TestWriteImportedHistoryBindsActivation(t *testing.T) {
	cfg, prepare := boundedActivationSourceFixture(t)
	writes := 0
	cfg, request := activationPreparedFixtureWith(t, cfg, prepare, func(history recovery.WriteHistoryRequest) recovery.HistoryWriteReport {
		t.Helper()
		writes++
		writeHistoryRefusals(t, cfg, history)
		report, err := WriteImportedHistory(t.Context(), cfg, history)
		require.NoError(t, err)
		return report
	})
	require.Equal(t, 1, writes)
	// The writer changes no target store, so every import barrier stays closed.
	_, err := storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	activated, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, activated.HistoricallyComplete)
	require.True(t, activated.CurrentAdmissionValid)
	require.False(t, activated.Restricted)
}

// writeHistoryRefusals checks each refusal against the prepared target before the accepted write.
// No refusal leaves a package behind.
func writeHistoryRefusals(t *testing.T, cfg *config.Config, request recovery.WriteHistoryRequest) {
	t.Helper()
	directory := request.History.Directory
	requireEmpty := func() {
		t.Helper()
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Empty(t, entries)
	}

	// The command refuses an unfenced attestation before it loads configuration.
	var output bytes.Buffer
	loads := 0
	deps := localOperatorDependencies(t, cfg, &output)
	deps.LoadConfig = func(context.Context) (*config.Config, error) {
		loads++
		return cfg, nil
	}
	arguments := writeHistoryArguments(request)
	arguments[slices.Index(arguments, "--writers-fenced=true")] = "--writers-fenced=false"
	err := cli.Run(t.Context(), arguments, deps)
	require.Error(t, err)
	require.Equal(t, cli.ExitCodeUsage, cli.ExitCode(err))
	require.Zero(t, loads)
	require.NotContains(t, output.String(), "history_sha256")
	requireEmpty()

	unfenced := request
	unfenced.History.Attestation.WritersFenced = false
	_, err = WriteImportedHistory(t.Context(), cfg, unfenced)
	require.Error(t, err)
	requireEmpty()

	digest := request
	digest.ManifestSHA256 = strings.Repeat("0", 64)
	_, err = WriteImportedHistory(t.Context(), cfg, digest)
	require.Error(t, err)
	requireEmpty()

	// A receipt from another target state cannot bind this target.
	drifted := request
	drifted.ExpectedTargetSHA256 = strings.Repeat("0", 64)
	_, err = WriteImportedHistory(t.Context(), cfg, drifted)
	require.ErrorIs(t, err, recovery.ErrConflict)
	requireEmpty()

	notes := filepath.Join(directory, "operator-notes.txt")
	require.NoError(t, os.WriteFile(notes, []byte("retained"), 0o600))
	_, err = WriteImportedHistory(t.Context(), cfg, request)
	require.ErrorIs(t, err, recovery.ErrConflict)
	body, err := os.ReadFile(notes)
	require.NoError(t, err)
	require.Equal(t, "retained", string(body))
	require.NoFileExists(t, filepath.Join(directory, "history.json"))
	require.NoError(t, os.Remove(notes))
	requireEmpty()
}
