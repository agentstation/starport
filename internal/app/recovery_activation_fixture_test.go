package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// activationFleetFixture uses actual runtime capture and native isolated target owners.
// The controlled source has no post-backup activity. Its accepted package still rotates both authorities.
func activationFleetFixture(t *testing.T) (*config.Config, RecoveryActivationRequest) {
	t.Helper()
	valkey, postgres, endpoint := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if valkey == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: complete activation needs native Valkey, PostgreSQL, and object storage")
	}
	cfg, prepare := boundedActivationSourceFixture(t)
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	return activationPreparedFixture(t, cfg, prepare)
}

func activationPreparedFixture(t *testing.T, cfg *config.Config, prepare recovery.PrepareRequest) (*config.Config, RecoveryActivationRequest) {
	t.Helper()
	return activationPreparedFixtureWith(t, cfg, prepare, activationHistoryWriter(t, cfg))
}

// activationPreparedFixtureWith prepares the target and writes H through the selected shipped writer.
func activationPreparedFixtureWith(t *testing.T, cfg *config.Config, prepare recovery.PrepareRequest, write activationHistoryWrite) (*config.Config, RecoveryActivationRequest) {
	t.Helper()
	_, err := PrepareBackup(t.Context(), cfg, prepare)
	require.NoError(t, err)
	canonicalSelectedInputs(t, cfg)
	return cfg, activationHistoryWith(t, cfg, prepare, write)
}

// activationHistoryWrite writes one final-only history package and returns the shipped receipt.
type activationHistoryWrite func(recovery.WriteHistoryRequest) recovery.HistoryWriteReport

// activationHistoryWriter calls the shipped in-process writer.
func activationHistoryWriter(t *testing.T, cfg *config.Config) activationHistoryWrite {
	return func(request recovery.WriteHistoryRequest) recovery.HistoryWriteReport {
		t.Helper()
		report, err := WriteImportedHistory(t.Context(), cfg, request)
		require.NoError(t, err)
		return report
	}
}

// activationHistoryCommand drives the backup write-history verb in process.
func activationHistoryCommand(t *testing.T, deps cli.Dependencies, output *bytes.Buffer) activationHistoryWrite {
	return func(request recovery.WriteHistoryRequest) recovery.HistoryWriteReport {
		t.Helper()
		output.Reset()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		require.NoError(t, cli.Run(ctx, writeHistoryArguments(request), deps))
		var report recovery.HistoryWriteReport
		require.NoError(t, json.Unmarshal(output.Bytes(), &report))
		output.Reset()
		return report
	}
}

// writeHistoryArguments states one history request as backup write-history arguments.
func writeHistoryArguments(request recovery.WriteHistoryRequest) []string {
	history := request.History
	arguments := []string{operatorProgram, operatorBackup, "write-history", "--directory", request.Directory, "--manifest-sha256", request.ManifestSHA256,
		"--operation", history.Operation.ID, "--fencing-evidence", history.Operation.FencingEvidence, "--history-directory", history.Directory,
		"--mode", history.Mode, "--disposition", history.Disposition, "--through", history.Through.Format(time.RFC3339Nano), "--end-reference", history.EndReference,
		"--highest-epoch", strconv.FormatInt(history.HighestEpoch, 10), "--epoch-reference", history.EpochReference, "--epoch-operator", history.EpochOperator, "--epoch-evidence", history.EpochEvidence,
		"--operator", history.Attestation.Operator, "--attestation-reference", history.Attestation.Reference,
		"--writers-fenced=" + strconv.FormatBool(history.Attestation.WritersFenced), "--admitted-work-accounted=" + strconv.FormatBool(history.Attestation.AdmittedWorkAccounted),
		"--complete-interval=" + strconv.FormatBool(history.Attestation.CompleteInterval), operatorJSONFlag}
	for _, evidence := range history.Evidence {
		arguments = append(arguments, "--evidence-file", evidence.ID+"="+evidence.Path+"="+evidence.Reference)
	}
	for _, option := range [][2]string{{"--scratch", request.ScratchDirectory}, {"--valkey-incarnation", request.ValkeyIncarnation}, {"--expected-target-sha256", request.ExpectedTargetSHA256}} {
		if option[1] != "" {
			arguments = append(arguments, option[0], option[1])
		}
	}
	return arguments
}

// activationHistoryFixture writes the final-only history H that binds the source's import identity and the current target.
func activationHistoryFixture(t *testing.T, cfg *config.Config, prepare recovery.PrepareRequest) RecoveryActivationRequest {
	t.Helper()
	return activationHistoryWith(t, cfg, prepare, activationHistoryWriter(t, cfg))
}

// activationHistoryWith states the controlled source facts and lets the shipped writer derive both bindings.
func activationHistoryWith(t *testing.T, cfg *config.Config, prepare recovery.PrepareRequest, write activationHistoryWrite) RecoveryActivationRequest {
	t.Helper()
	incarnation := ""
	if cfg.RuntimeStorage().Type == storage.StorageTypeValkey {
		store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		incarnation, err = store.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
		require.NoError(t, err)
		require.NoError(t, store.Close())
	}
	root := filepath.Dir(prepare.FilesDirectory)
	history, journal, activation, scratch, evidence := filepath.Join(root, "independent-history"), filepath.Join(root, "history-journal"), filepath.Join(root, "activation"), filepath.Join(root, "scratch"), filepath.Join(root, "history-evidence")
	for _, path := range []string{history, journal, activation, scratch, evidence} {
		_, err := productfiles.CreateDirectory(path)
		require.NoError(t, err)
	}
	retained, err := productfiles.ExistingDirectory(evidence)
	require.NoError(t, err)
	require.NoError(t, retained.CompareAndPublish(t.Context(), "controlled-source.log", nil, []byte("controlled source stopped after the backup\n")))
	manifestBody, err := os.ReadFile(filepath.Join(prepare.Directory, "backup-manifest.json"))
	require.NoError(t, err)
	var backup recovery.BundleManifest
	require.NoError(t, json.Unmarshal(manifestBody, &backup))
	attestation := recovery.HistoryAttestation{Operator: "operator", Reference: "controlled-source-owner", WritersFenced: true, AdmittedWorkAccounted: true, CompleteInterval: true}
	verify := prepare.VerifyRequest
	// Both request owners must select the same original verification contract and private scratch.
	verify.ScratchDirectory = scratch
	// The prepared boundary is one epoch above the backup boundary. The source used two more epochs.
	written := write(recovery.WriteHistoryRequest{VerifyRequest: verify, ValkeyIncarnation: incarnation, History: recovery.HistoryWriteRequest{
		Directory: history, Operation: prepare.Operation, Mode: "planned_migration", Disposition: "replay_complete",
		Through: backup.FinishedAt.Add(time.Second), EndReference: "controlled-source-stopped",
		HighestEpoch: backup.Request.Boundary.Epoch + 4, EpochReference: "controlled-source-epoch", EpochOperator: "operator", EpochEvidence: "controlled-source",
		Evidence:    []recovery.HistoryEvidenceFile{{ID: "controlled-source", Path: filepath.Join(evidence, "controlled-source.log"), Reference: "controlled-source-owner"}},
		Attestation: attestation,
	}})
	require.Equal(t, history, written.Directory)
	require.Equal(t, 2, written.DeclaredSteps)
	request := RecoveryActivationRequest{Prepare: prepare, History: recovery.ApplyHistoryRequest{VerifyRequest: verify, Operation: prepare.Operation, HistoryDirectory: history, HistorySHA256: written.HistorySHA256, ExpectedTargetSHA256: written.TargetSHA256, JournalDirectory: journal, ValkeyIncarnation: incarnation, Attestation: attestation}, ActivationDirectory: activation, PreserveTargetWorkspace: true}
	request.Prepare.ScratchDirectory = scratch
	if cfg.RuntimeStorage().Type == storage.StorageTypeValkey {
		t.Cleanup(func() {
			store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
			require.NoError(t, err)
			keys, err := store.ScanWithPrefix(context.Background(), "", 10000)
			require.NoError(t, err)
			if len(keys) > 0 {
				require.NoError(t, store.BatchDelete(context.Background(), keys))
			}
			require.NoError(t, store.Close())
		})
	}
	return request
}

// activationFromActivatedFleet captures actual fleet publications from a completed native target.
func activationFromActivatedFleet(t *testing.T, destinationFleet bool) (*config.Config, RecoveryActivationRequest) {
	t.Helper()
	source, original := activationFleetFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	activated, err := ActivateRecovery(ctx, source, original)
	require.NoError(t, err)
	require.True(t, activated.CurrentAdmissionValid)
	_, err = CloseBackupBoundary(ctx, source)
	require.NoError(t, err)
	captureParent := filepath.Join(t.TempDir(), "private-capture")
	_, err = productfiles.CreateDirectory(captureParent)
	require.NoError(t, err)
	capture := recovery.CaptureRequest{Destination: filepath.Join(captureParent, "fleet-backup"), Build: "test", OperationID: "capture-fleet", FencingEvidence: "controlled-source-stopped", KeyReference: "test-master-key"}
	receipt, err := CaptureBackup(ctx, source, capture)
	require.NoError(t, err)
	parent := filepath.Join(t.TempDir(), "target")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": source.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": source.EffectivePaths().DeploymentID, "STARPORT_INSTANCE_ID": "restored-replica"}
	for key, value := range source.Catalog.CatalogValues() {
		if key != "STARMAP_STATE_DIR" {
			values["STARPORT_"+strings.TrimPrefix(key, "STARMAP_")] = value
		}
	}
	target, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(values).Load(ctx)
	require.NoError(t, err)
	if destinationFleet {
		destinationKV, err := url.Parse(os.Getenv("TEST_VALKEY_URL"))
		require.NoError(t, err)
		// A restored fleet uses a distinct native import scope from its captured source.
		destinationKV.Path = "/9"
		configureSharedRestore(t, target, destinationKV.String(), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT"))
	}
	prepare := recovery.PrepareRequest{VerifyRequest: recovery.VerifyRequest{Directory: receipt.Directory, ManifestSHA256: receipt.ManifestSHA256}, FilesDirectory: filepath.Join(parent, "prepared"), Operation: recovery.RestoreOperation{ID: "restore-fleet", FencingEvidence: "controlled-source-stopped"}}
	return activationPreparedFixture(t, target, prepare)
}
