package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const measurementDirectoryEnvironment = "STARPORT_RECOVERY_MEASUREMENT_DIR"

// TestRecoveryMeasurement records three idle fleet recovery scenarios with three repetitions each.
// The lead wrapper supplies native fixtures and the source-built shipping executable.
func TestRecoveryMeasurement(t *testing.T) {
	directory := os.Getenv(measurementDirectoryEnvironment)
	if directory == "" {
		t.Skip("UNVERIFIED: STARPORT_RECOVERY_MEASUREMENT_DIR is unset; recovery measurement did not run")
	}
	require.True(t, filepath.IsAbs(directory), "measurement directory must be absolute")
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	relative, err := filepath.Rel(root, directory)
	require.NoError(t, err)
	require.True(t, relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)), "measurement output must be outside the worktree")
	_, err = productfiles.CreateDirectory(directory)
	require.NoError(t, err, "use a new private measurement directory")
	private := filepath.Join(directory, "private")
	_, err = productfiles.CreateDirectory(private)
	require.NoError(t, err)
	for _, name := range []string{"TEST_POSTGRES_URL", "TEST_BLOB_S3_ENDPOINT", "STARPORT_RECOVERY_OPERATOR_BINARY"} {
		require.NotEmpty(t, os.Getenv(name), "required fixture input is absent: %s", name)
	}
	binary := populatedBinary(t)
	record := measurementMetadata(t, binary)
	t.Cleanup(func() { measurementWrite(t, directory, record) })
	for _, scenario := range []string{"empty-target-import", "valkey-restart-persistent", "replica-promotion-acknowledged-loss"} {
		for repeat := 1; repeat <= 3; repeat++ {
			passed := t.Run(fmt.Sprintf("%s/%d", scenario, repeat), func(t *testing.T) {
				run := &measurementRun{Scenario: scenario, Repeat: repeat, Load: "idle; two explicit sequential Valkey probe writes before fence",
					FenceScope: "test-owned writers closed before capture; no gateway or workers run before restored readiness; old primary remains isolated"}
				record.Runs = append(record.Runs, run)
				t.Cleanup(func() { run.Complete = !t.Failed() && !run.StateObservedAt.IsZero() })
				child := filepath.Join(private, fmt.Sprintf("%s-%d", scenario, repeat))
				_, err := productfiles.CreateDirectory(child)
				require.NoError(t, err)
				f := populatedAdoptionFixture(t)
				record.Topology.Containers["valkey"] = measurementContainerImage(t, f.valkey.name)
				measurementConfigureListener(t, f)
				run.PriorApproval = f.prior
				run.ValkeyBefore = adoptIncarnation(t, f.cfg, f.valkey.url)
				keys := []string{"measurement:retained", "measurement:acknowledged"}
				measurementAcknowledge(t, f.cfg, keys[:1])
				var oldConfig *config.Config
				if scenario == "replica-promotion-acknowledged-loss" {
					replica, address := f.valkey.replica(t)
					f.valkey.promote(t, replica)
					measurementAcknowledge(t, f.cfg, keys[1:])
					oldConfig = f.cfg
					f.environment["STARPORT_STORAGE_VALKEY_URL"] = address
					f.cfg, err = config.NewLoader().WithEnvironment(f.environment).Load(t.Context())
					require.NoError(t, err, "cannot load promoted configuration")
				} else {
					measurementAcknowledge(t, f.cfg, keys[1:])
				}
				run.Acknowledged = len(keys)
				// No writer remains open after the probe writes. This instant starts the external test fence.
				run.OutageStart = time.Now().UTC()
				if scenario == "valkey-restart-persistent" {
					f.valkey.restart(t)
					require.NotEqual(t, run.ValkeyBefore, adoptIncarnation(t, f.cfg, f.valkey.url))
				}
				if scenario != "empty-target-import" {
					measurementRefusedAuthority(t, f.cfg)
				}
				capture := measurementCapture(t, binary, f, child, run)
				if scenario == "empty-target-import" {
					measurementImport(t, binary, f, capture, child, run)
				} else {
					measurementAdopt(t, binary, f, capture, child, run)
				}
				measurementReadiness(t, binary, f, child, run)
				run.RestoredApproval = populatedApproved(t, f.cfg, f.prior)
				run.ValkeyRestored = adoptIncarnation(t, f.cfg, f.cfg.RuntimeStorage().Valkey.URL)
				require.Equal(t, run.ValkeyRestored, run.RestoredApproval.BackendID)
				run.Present = measurementPresent(t, f.cfg, keys)
				run.Lost = run.Acknowledged - run.Present
				run.StateObservedAt = time.Now().UTC()
				run.OutageToStateSeconds = run.StateObservedAt.Sub(run.OutageStart).Seconds()
				if scenario == "replica-promotion-acknowledged-loss" {
					require.Equal(t, 1, run.Lost, "promoted primary must retain the measured acknowledged loss")
					require.Equal(t, 2, measurementPresent(t, oldConfig, keys), "reachable old primary must retain both acknowledged writes")
					measurementRefusedAuthority(t, oldConfig)
				} else {
					require.Zero(t, run.Lost)
				}
			})
			if !passed {
				t.Fatal("recovery measurement stopped after a failed run; partial record retained")
			}
		}
	}
	require.Len(t, record.Runs, 9)
	for _, run := range record.Runs {
		require.True(t, run.Complete, "each measurement must complete without skips")
	}
}

func measurementConfigureListener(t *testing.T, f *populatedFixture) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).AddrPort().Port()
	require.NoError(t, listener.Close())
	f.environment["STARPORT_SERVER_HOST"] = "127.0.0.1"
	f.environment["STARPORT_SERVER_PORT"] = strconv.Itoa(int(port))
	f.cfg, err = config.NewLoader().WithEnvironment(f.environment).Load(t.Context())
	require.NoError(t, err, "cannot load gateway listener configuration")
}

func measurementAcknowledge(t *testing.T, cfg *config.Config, keys []string) {
	t.Helper()
	store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err, "cannot open probe writer")
	for _, key := range keys {
		require.NoError(t, store.Set(t.Context(), key, []byte(key)), "probe write was not acknowledged")
	}
	require.NoError(t, store.Close())
}

func measurementPresent(t *testing.T, cfg *config.Config, keys []string) int {
	t.Helper()
	store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err, "cannot inspect restored probe state")
	defer func() { require.NoError(t, store.Close()) }()
	count := 0
	for _, key := range keys {
		body, err := store.Get(t.Context(), key)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		require.NoError(t, err, "cannot read restored probe state")
		require.True(t, bytes.Equal(body, []byte(key)), "restored probe value changed")
		count++
	}
	return count
}

func measurementRefusedAuthority(t *testing.T, cfg *config.Config) {
	t.Helper()
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	witness, err := recovery.New(db)
	require.NoError(t, err)
	store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	_, err = witness.OpenAuthority(t.Context(), store.(storage.IncarnationProvider), cfg.EffectivePaths().DeploymentID)
	require.Error(t, err, "changed or old Valkey identity must refuse authority")
}

func measurementCommandRun(t *testing.T, binary string, f *populatedFixture, private string, run *measurementRun, name string, arguments ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...) // #nosec G204 G702 -- The lead supplies the source-bound shipping binary.
	command.Env = operatorChildEnvironment(f.environment)
	command.Dir = f.cfg.EffectivePaths().ConfigDir
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	started := time.Now().UTC()
	err := command.Run()
	finished := time.Now().UTC()
	status := 0
	if err != nil {
		status = -1
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			status = exit.ExitCode()
		}
	}
	output := operatorPrivateJSON(t, private, name+"-output.json", map[string][]byte{"stdout": stdout.Bytes(), "stderr": stderr.Bytes()})
	run.Commands = append(run.Commands, measurementCommand{Name: name, StartedAt: started, FinishedAt: finished,
		DurationSeconds: finished.Sub(started).Seconds(), ExitStatus: status,
		Output: filepath.Join("private", filepath.Base(private), filepath.Base(output))})
	require.NoError(t, err, "shipping command %s failed; output is private", name)
	return stdout.Bytes()
}

func measurementCapture(t *testing.T, binary string, f *populatedFixture, private string, run *measurementRun) recovery.CaptureResult {
	t.Helper()
	body := measurementCommandRun(t, binary, f, private, run, "close", operatorBackup, "close", operatorJSONFlag)
	require.NoError(t, json.Unmarshal(body, &run.ClosedBoundary))
	body = measurementCommandRun(t, binary, f, private, run, "capture", operatorBackup, "create", "--destination", filepath.Join(private, "capture"),
		"--operation", "measurement-capture", "--fencing-evidence", "measurement-test-writers-fenced", "--key-reference", "test-master-key", operatorJSONFlag)
	var receipt recovery.CaptureResult
	require.NoError(t, json.Unmarshal(body, &receipt))
	return receipt
}

func measurementAdopt(t *testing.T, binary string, f *populatedFixture, capture recovery.CaptureResult, private string, run *measurementRun) {
	t.Helper()
	f.adoption = filepath.Join(private, "adoption")
	_, err := productfiles.CreateDirectory(f.adoption)
	require.NoError(t, err)
	state := filepath.Join(f.adoption, "catalog-state")
	_, err = productfiles.CreateDirectory(state)
	require.NoError(t, err)
	f.environment[operatorCatalogStateEnvironment] = state
	f.cfg, err = config.NewLoader().WithEnvironment(f.environment).Load(t.Context())
	require.NoError(t, err)
	prepare := recovery.PrepareRequest{VerifyRequest: recovery.VerifyRequest{Directory: capture.Directory, ManifestSHA256: capture.ManifestSHA256},
		FilesDirectory: filepath.Join(f.adoption, "prepared"), Operation: recovery.RestoreOperation{ID: "measurement-adopt", FencingEvidence: "measurement-test-writers-fenced"}}
	preparation := filepath.Join(f.adoption, "preparation")
	_, err = productfiles.CreateDirectory(preparation)
	require.NoError(t, err)
	activation := activationHistoryWith(t, f.cfg, prepare, measurementWriteHistory(t, binary, f, private, run, "adopt-write-history", ""))
	measurementHistory(t, f.cfg, &activation, run)
	f.request = PopulatedRecoveryRequest{Activation: activation, PriorApproval: f.prior, PreparationDirectory: preparation}
	f.requestFile = operatorPrivateJSON(t, private, "adoption-request.json", f.request)
	body := measurementCommandRun(t, binary, f, private, run, "adopt-prepare", populatedArguments(populatedPrepare, f.requestFile, "", "")[1:]...)
	run.PreparedSHA256 = populatedPrepared(t, body)
	run.PreparedBoundary = populatedWitness(t, f.cfg).current
	body = measurementCommandRun(t, binary, f, private, run, "adopt-activate", populatedArguments(populatedActivate, f.requestFile, run.PreparedSHA256, "")[1:]...)
	completed := populatedResult(t, body)
	require.True(t, completed.HistoricallyComplete)
	require.True(t, completed.CurrentAdmissionValid)
	require.False(t, completed.Restricted)
	run.DecisionSHA256 = completed.DecisionSHA256
	body = measurementCommandRun(t, binary, f, private, run, "adopt-inspect", populatedArguments(populatedInspect, f.requestFile, run.PreparedSHA256, run.DecisionSHA256)[1:]...)
	require.Equal(t, completed, populatedResult(t, body))
}

func measurementImport(t *testing.T, binary string, f *populatedFixture, capture recovery.CaptureResult, private string, run *measurementRun) {
	t.Helper()
	parent := filepath.Join(private, "target")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	values := map[string]string{operatorMasterKeyEnvironment: f.cfg.Security.MasterKey,
		operatorDeploymentEnvironment: f.cfg.EffectivePaths().DeploymentID, operatorInstanceEnvironment: "measurement-restored"}
	for key, value := range f.cfg.Catalog.CatalogValues() {
		if key != operatorProducerStateEnvironment {
			values["STARPORT_"+strings.TrimPrefix(key, "STARMAP_")] = value
		}
	}
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	target := startAdoptValkey(t)
	configureSharedRestore(t, cfg, target.url, os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT"))
	canonicalSelectedInputs(t, cfg)
	f.cfg, f.environment = operatorPrimaryConfiguration(t, cfg)
	measurementConfigureListener(t, f)
	prepare := recovery.PrepareRequest{VerifyRequest: recovery.VerifyRequest{Directory: capture.Directory, ManifestSHA256: capture.ManifestSHA256},
		FilesDirectory: filepath.Join(parent, "prepared"), Operation: recovery.RestoreOperation{ID: "measurement-import", FencingEvidence: "measurement-test-writers-fenced"}}
	arguments := []string{operatorBackup, populatedPrepare, "--directory", prepare.Directory, "--manifest-sha256", prepare.ManifestSHA256,
		"--files-directory", prepare.FilesDirectory, "--operation", prepare.Operation.ID, "--fencing-evidence", prepare.Operation.FencingEvidence, operatorJSONFlag}
	body := measurementCommandRun(t, binary, f, private, run, "prepare", arguments...)
	var prepared recovery.PrepareResult
	require.NoError(t, json.Unmarshal(body, &prepared))
	boundary := prepared.Prepared.Boundary
	run.PreparedBoundary = boundary
	incarnation := adoptIncarnation(t, f.cfg, target.url)
	body = measurementCommandRun(t, binary, f, private, run, "inspect-import", operatorBackup, "inspect-import",
		"--directory", prepare.Directory, "--manifest-sha256", prepare.ManifestSHA256, "--operation", prepare.Operation.ID,
		"--fencing-evidence", prepare.Operation.FencingEvidence, "--destination", filepath.Join(private, "inspection"),
		"--expected-deployment", boundary.DeploymentID, "--expected-recovery-epoch", strconv.FormatInt(boundary.Epoch, 10),
		"--expected-recovery-evidence", boundary.Evidence, "--expected-recovery-backend", boundary.BackendID,
		"--kv-replay-sequence", "0", "--sql-replay-sequence", "0", "--blob-replay-sequence", "0", "--valkey-incarnation", incarnation, operatorJSONFlag)
	var inspected recovery.ImportInspectionResult
	require.NoError(t, json.Unmarshal(body, &inspected))
	run.InspectionSHA256 = inspected.Inspection.RequestSHA256
	activation := activationHistoryWith(t, f.cfg, prepare, measurementWriteHistory(t, binary, f, private, run, "write-history", inspected.TargetSHA256))
	require.Equal(t, inspected.TargetSHA256, activation.History.ExpectedTargetSHA256)
	measurementHistory(t, f.cfg, &activation, run)
	request := operatorPrivateJSON(t, private, "activation-request.json", activation)
	body = measurementCommandRun(t, binary, f, private, run, operatorActivate, operatorCommandArguments(operatorActivate, request, "")[1:]...)
	completed := populatedResult(t, body)
	require.True(t, completed.HistoricallyComplete)
	require.True(t, completed.CurrentAdmissionValid)
	require.False(t, completed.Restricted)
	run.DecisionSHA256 = completed.DecisionSHA256
}

// measurementWriteHistory writes H with the shipping binary. The final-only package covers the test fence and capture,
// including deliberately lost probe writes.
func measurementWriteHistory(t *testing.T, binary string, f *populatedFixture, private string, run *measurementRun, name, expectedTarget string) activationHistoryWrite {
	return func(request recovery.WriteHistoryRequest) recovery.HistoryWriteReport {
		t.Helper()
		request.History.Through = time.Now().UTC()
		request.History.EndReference = "measurement-test-writers-fenced"
		request.ExpectedTargetSHA256 = expectedTarget
		body := measurementCommandRun(t, binary, f, private, run, name, writeHistoryArguments(request)[1:]...)
		var report recovery.HistoryWriteReport
		require.NoError(t, json.Unmarshal(body, &report))
		run.HistoryThrough = request.History.Through
		return report
	}
}

func measurementHistory(t *testing.T, cfg *config.Config, request *RecoveryActivationRequest, run *measurementRun) {
	t.Helper()
	run.HistorySHA256 = request.History.HistorySHA256
	run.HistoryCapturedAt = time.Now().UTC()
	run.HistoryAgeSeconds = run.OutageStart.Sub(run.HistoryCapturedAt).Seconds()
	run.Fencing = request.History.Attestation
	run.FencingEvidence = request.Prepare.Operation.FencingEvidence
	body, err := os.ReadFile(filepath.Join(request.Prepare.Directory, "backup-manifest.json"))
	require.NoError(t, err)
	var backup recovery.BundleManifest
	require.NoError(t, json.Unmarshal(body, &backup))
	run.BackupStartedAt, run.BackupFinishedAt = backup.StartedAt.UTC(), backup.FinishedAt.UTC()
	run.BackupAgeSeconds = run.OutageStart.Sub(run.BackupFinishedAt).Seconds()
	run.BackupSHA256 = request.Prepare.ManifestSHA256
	encryption, err := backupEncryption(cfg)
	require.NoError(t, err)
	source, err := recovery.InspectRestoreSource(t.Context(), request.Prepare.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	require.NoError(t, err)
	identity, err := source.ImportIdentity(request.Prepare.Operation)
	require.NoError(t, err)
	run.SQLSnapshotSHA256 = identity.SQLOriginal.SHA256
	run.BlobSnapshotSHA256 = identity.BlobOriginal.SHA256
}

func measurementReadiness(t *testing.T, binary string, f *populatedFixture, private string, run *measurementRun) {
	t.Helper()
	// Cleanup owns the gateway lifetime. Test cancellation must not kill it before graceful shutdown.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 3*time.Minute)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, binary, "serve") // #nosec G204 -- The lead supplies the source-bound shipping binary.
	command.Env = operatorChildEnvironment(f.environment)
	command.Dir = f.cfg.EffectivePaths().ConfigDir
	output, err := os.OpenFile(filepath.Join(private, "gateway-output.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, output.Close()) })
	command.Stdout, command.Stderr = output, output
	run.GatewayStart = time.Now().UTC()
	require.NoError(t, command.Start(), "shipping gateway cannot start; private output retained")
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			require.NoError(t, err, "gateway did not stop cleanly; private output retained")
		case <-time.After(30 * time.Second):
			cancel()
			<-done
			t.Error("gateway shutdown exceeded its deadline")
		}
	})
	client := &http.Client{Timeout: time.Second}
	address := fmt.Sprintf("http://127.0.0.1:%d/health/ready", f.cfg.Server.Port)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			done <- err
			t.Fatal("shipping gateway exited before readiness; inspect private output")
		case <-ctx.Done():
			t.Fatal("shipping gateway readiness deadline expired; inspect private output")
		default:
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		require.NoError(t, err)
		response, err := client.Do(request)
		if err == nil {
			observed := time.Now().UTC()
			require.NoError(t, response.Body.Close())
			if response.StatusCode == http.StatusOK {
				run.ReadinessHTTPStatus = response.StatusCode
				run.ReadinessRestored = observed
				run.OutageToReadySeconds = run.ReadinessRestored.Sub(run.OutageStart).Seconds()
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("shipping gateway readiness deadline expired; inspect private output")
		case <-ticker.C:
		}
	}
}
