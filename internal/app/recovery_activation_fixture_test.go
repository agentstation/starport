package app

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/catalog"
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
	_, err := PrepareBackup(t.Context(), cfg, prepare)
	require.NoError(t, err)
	canonicalSelectedInputs(t, cfg)
	return cfg, activationHistoryFixture(t, cfg, prepare)
}

// activationHistoryFixture writes the final-only history H that binds the source's import identity and the current target.
func activationHistoryFixture(t *testing.T, cfg *config.Config, prepare recovery.PrepareRequest) RecoveryActivationRequest {
	t.Helper()
	incarnation := ""
	if cfg.RuntimeStorage().Type == storage.StorageTypeValkey {
		store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		incarnation, err = store.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
		require.NoError(t, err)
		require.NoError(t, store.Close())
	}
	encryption, err := backupEncryption(cfg)
	require.NoError(t, err)
	source, err := recovery.InspectRestoreSource(t.Context(), prepare.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	require.NoError(t, err)
	identity, err := source.ImportIdentity(prepare.Operation)
	require.NoError(t, err)
	view, err := source.OpenCapturedKV(t.Context())
	require.NoError(t, err)
	_, kvExpected, err := revision.CaptureKVRecovery(t.Context(), view)
	require.NoError(t, err)
	require.NoError(t, view.Close())
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	sqlExpected, err := revision.CaptureSQLRecovery(t.Context(), db, conn)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	blobs, err := restoreBlobTarget(t.Context(), cfg.Files)
	require.NoError(t, err)
	target, err := configuredRecoveryTarget(t.Context(), cfg, db, blobs, incarnation)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	root := filepath.Dir(prepare.FilesDirectory)
	history, journal, activation, scratch := filepath.Join(root, "independent-history"), filepath.Join(root, "history-journal"), filepath.Join(root, "activation"), filepath.Join(root, "scratch")
	for _, path := range []string{history, journal, activation, scratch, filepath.Join(history, "payloads")} {
		_, err := productfiles.CreateDirectory(path)
		require.NoError(t, err)
	}
	var steps []map[string]any
	payloads := []any{map[string]any{"version": 1, "expected_sha256": kvExpected}, map[string]any{"version": 1, "expected": sqlExpected}}
	for index, payload := range payloads {
		body, err := json.Marshal(payload, json.Deterministic(true))
		require.NoError(t, err)
		path := fmt.Sprintf("payloads/%06d.json", index+1)
		require.NoError(t, os.WriteFile(filepath.Join(history, path), body, 0600))
		kind := "kv_authorization_final"
		if index == 1 {
			kind = "sql_authorization_final"
		}
		steps = append(steps, map[string]any{"ordinal": index + 1, "kind": kind, "path": path, "size": len(body), "sha256": canonicalRecordSHA256(body), "evidence_source_ids": []string{"controlled-source"}})
	}
	manifestBody, err := os.ReadFile(filepath.Join(prepare.Directory, "backup-manifest.json"))
	require.NoError(t, err)
	var backup recovery.BundleManifest
	require.NoError(t, json.Unmarshal(manifestBody, &backup))
	encodedIdentity, err := json.Marshal(identity, json.Deterministic(true))
	require.NoError(t, err)
	historyBody, err := json.Marshal(map[string]any{
		"version": 1, "backup_sha256": prepare.ManifestSHA256, "deployment_id": source.DeploymentID(), "operation": prepare.Operation, "target_sha256": target, "prepared_sha256": canonicalRecordSHA256(encodedIdentity),
		"mode": "planned_migration", "disposition": "replay_complete",
		"interval":         map[string]any{"through_utc": backup.FinishedAt.Add(time.Second), "end_reference": "controlled-source-stopped"},
		"highest_epoch":    recovery.EpochEvidence{HighestEpoch: identity.Boundary.Epoch + 3, SourceSHA256: strings.Repeat("e", 64), Reference: "controlled-source-epoch", Operator: "operator"},
		"evidence_sources": []map[string]any{{"id": "controlled-source", "sha256": strings.Repeat("e", 64), "size": 5, "reference": "controlled-source-owner"}}, "steps": steps,
	}, json.Deterministic(true))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(history, "history.json"), historyBody, 0600))
	request := RecoveryActivationRequest{Prepare: prepare, History: recovery.ApplyHistoryRequest{VerifyRequest: prepare.VerifyRequest, Operation: prepare.Operation, HistoryDirectory: history, HistorySHA256: canonicalRecordSHA256(historyBody), ExpectedTargetSHA256: target, JournalDirectory: journal, ValkeyIncarnation: incarnation, Attestation: recovery.HistoryAttestation{Operator: "operator", Reference: "controlled-source-owner", WritersFenced: true, AdmittedWorkAccounted: true, CompleteInterval: true}}, ActivationDirectory: activation, PreserveTargetWorkspace: true}
	// Both request owners must select the same original verification contract and private scratch.
	request.Prepare.ScratchDirectory = scratch
	request.History.ScratchDirectory = scratch
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
