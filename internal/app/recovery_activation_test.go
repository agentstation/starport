package app

import (
	"context"
	legacyjson "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func sealActivationFixture(t *testing.T, cfg *config.Config, request RecoveryActivationRequest) (*sealedRecoveryActivation, *recoveryActivationNative) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	t.Cleanup(cancel)
	encryption, err := backupEncryption(cfg)
	require.NoError(t, err)
	source, err := recovery.InspectRestoreSource(ctx, request.Prepare.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	require.NoError(t, err)
	native, err := openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	directory, err := productfiles.ExistingDirectory(request.ActivationDirectory)
	require.NoError(t, err)
	require.NoError(t, checkUnsealedActivation(ctx, cfg, native, source, request))
	sealed, err := prepareRecoveryActivationDecision(ctx, cfg, native, source, request, directory)
	if err != nil {
		_ = native.close()
	}
	require.NoError(t, err)
	return sealed, native
}

func commitActivationNativePhases(t *testing.T, cfg *config.Config, sealed *sealedRecoveryActivation, native *recoveryActivationNative, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	for index := 0; index < count; index++ {
		require.NoError(t, sealed.checkCurrent(ctx, cfg, native, false))
		switch index {
		case 0:
			require.NoError(t, native.blobActivate.ActivateImportAt(ctx, sealed.identity.ComponentOperation, sealed.identity.BlobOriginal, sealed.facts.Positions.Blobs, sealed.journal.Digest()))
		case 1:
			require.NoError(t, native.kvActivate.ActivateImportAt(ctx, sealed.identity.KVClaim, sealed.facts.Positions.KV, sealed.journal.Digest()))
		case 2:
			require.NoError(t, sealed.approveSQL(ctx, cfg, native))
		}
		// The last native reply is lost before the application writes its phase record.
		if index+1 < count {
			require.NoError(t, sealed.journal.PublishPhase(ctx, []recovery.ActivationPhase{recovery.ActivationBlobs, recovery.ActivationKV, recovery.ActivationSQL}[index]))
		}
	}
}

type activationChildFixture struct {
	Paths   config.Paths              `json:"paths"`
	Config  jsontext.Value            `json:"config"`
	Catalog map[string]string         `json:"catalog"`
	Request RecoveryActivationRequest `json:"request"`
}

func runActivationChild(t *testing.T, cfg *config.Config, request RecoveryActivationRequest) {
	t.Helper()
	body, err := json.Marshal(cfg, legacyjson.FormatDurationAsNano(true), json.Deterministic(true))
	require.NoError(t, err)
	fixture, err := json.Marshal(activationChildFixture{cfg.EffectivePaths(), body, cfg.Catalog.CatalogValues(), request}, json.Deterministic(true))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "activation-child.json")
	require.NoError(t, os.WriteFile(path, fixture, 0600))
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecoveryActivationFreshProcessNativeBoundaries$", "-test.v")
	command.Env = append(os.Environ(), "STARPORT_ACTIVATION_CHILD="+path)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

func TestRecoveryActivationFreshProcessNativeBoundaries(t *testing.T) {
	if path := os.Getenv("STARPORT_ACTIVATION_CHILD"); path != "" {
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		var fixture activationChildFixture
		require.NoError(t, json.Unmarshal(body, &fixture))
		var saved config.Config
		require.NoError(t, json.Unmarshal(fixture.Config, &saved, legacyjson.FormatDurationAsNano(true)))
		values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": saved.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": fixture.Paths.DeploymentID, "STARPORT_INSTANCE_ID": fixture.Paths.InstanceID}
		for key, value := range fixture.Catalog {
			name := "STARPORT_" + strings.TrimPrefix(key, "STARMAP_")
			if key == "STARMAP_STATE_DIR" {
				name = "STARPORT_CATALOG_STATE_DIR"
			}
			values[name] = value
		}
		cfg, err := config.NewLoader().WithPaths(fixture.Paths).WithEnvFiles().WithEnvironment(values).Load(t.Context())
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(fixture.Config, cfg, legacyjson.FormatDurationAsNano(true)))
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		result, err := ActivateRecovery(ctx, cfg, fixture.Request)
		require.NoError(t, err)
		require.Equal(t, 3, result.CompletedPhases)
		require.True(t, result.HistoricallyComplete)
		require.True(t, result.CurrentAdmissionValid)
		require.False(t, result.Restricted)
		require.Equal(t, fixture.Request.ExpectedDecisionSHA256, result.DecisionSHA256)
		return
	}
	for count := 0; count <= 3; count++ {
		t.Run([]string{"sealed", "blob-native-commit", "kv-native-commit", "sql-native-commit"}[count], func(t *testing.T) {
			cfg, request := activationFleetFixture(t)
			sealed, native := sealActivationFixture(t, cfg, request)
			request.ExpectedDecisionSHA256 = sealed.journal.Digest()
			commitActivationNativePhases(t, cfg, sealed, native, count)
			require.NoError(t, native.close())
			runActivationChild(t, cfg, request)
			body, err := os.ReadFile(filepath.Join(request.ActivationDirectory, "decision.json"))
			require.NoError(t, err)
			require.Equal(t, request.ExpectedDecisionSHA256, canonicalRecordSHA256(body))
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			status, err := InspectRecoveryActivation(ctx, cfg, request)
			require.NoError(t, err)
			require.True(t, status.HistoricallyComplete)
			require.True(t, status.CurrentAdmissionValid)
		})
	}
}

func TestRecoveryActivationCurrentChoiceOrOriginalEvidenceFailureKeepsRemainingOwnersClosed(t *testing.T) {
	for _, mode := range []string{"settings", "token", "source-file", "runtime", "original-history", "decision"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request := activationFleetFixture(t)
			sealed, native := sealActivationFixture(t, cfg, request)
			request.ExpectedDecisionSHA256 = sealed.journal.Digest()
			commitActivationNativePhases(t, cfg, sealed, native, 1)
			require.NoError(t, native.close())
			switch mode {
			case "settings":
				cfg.Server.Port++
			case "token":
				require.NoError(t, os.WriteFile(cfg.EffectivePaths().LocalTokenFile, []byte("invalid selected administrator token"), 0600))
			case "source-file":
				require.NoError(t, os.WriteFile(cfg.Catalog.SourceURL, []byte("changed selected source"), 0600))
			case "runtime":
				require.NoError(t, os.WriteFile(filepath.Join(cfg.EffectivePaths().RuntimeDir, "unexpected-runtime-state"), []byte("foreign state"), 0600))
			case "original-history":
				require.NoError(t, os.Remove(filepath.Join(request.History.HistoryDirectory, "payloads", "000001.json")))
			case "decision":
				require.NoError(t, os.WriteFile(filepath.Join(request.ActivationDirectory, "decision.json"), []byte("{}"), 0600))
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			_, err := ActivateRecovery(ctx, cfg, request)
			require.Error(t, err)
			native, err = openRecoveryActivationNative(ctx, cfg, request)
			require.NoError(t, err)
			defer func() { require.NoError(t, native.close()) }()
			current, err := native.witness.Current(ctx, sealed.facts.Boundary.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, sealed.facts.Boundary, current)
			require.ErrorIs(t, native.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
			require.NoError(t, native.kv.(storage.ImportUnreleasedInspector).CheckUnreleasedImport(ctx, sealed.identity.KVClaim))
		})
	}
}

func TestRecoveryActivationCompletedRetryPreservesLaterWithdrawal(t *testing.T) {
	cfg, request := activationFleetFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	result, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, result.CurrentAdmissionValid)
	request.ExpectedDecisionSHA256 = result.DecisionSHA256
	native, err := openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	current, err := native.witness.Current(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	withdrawn, err := native.witness.Close(ctx, current)
	require.NoError(t, err)
	require.NoError(t, native.close())
	result, err = ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, result.HistoricallyComplete)
	require.False(t, result.CurrentAdmissionValid)
	require.True(t, result.Restricted)
	require.Equal(t, "inspect current catalog permission and deployment approval", result.NextAction)
	native, err = openRecoveryActivationNative(ctx, cfg, request)
	require.NoError(t, err)
	defer func() { require.NoError(t, native.close()) }()
	current, err = native.witness.Current(ctx, cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.Equal(t, withdrawn, current)
}

func TestRecoveryActivationSQLApprovalRejectsChangedCurrentSource(t *testing.T) {
	cfg, request := activationFleetFixture(t)
	sealed, native := sealActivationFixture(t, cfg, request)
	defer func() { require.NoError(t, native.close()) }()
	commitActivationNativePhases(t, cfg, sealed, native, 2)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	require.NoError(t, sealed.checkExternalInputs(ctx, cfg, native))
	require.NoError(t, os.WriteFile(cfg.Catalog.SourceURL, []byte("changed selected source"), 0600))
	// The transaction must reject the changed source after external owner checks succeeded.
	require.Error(t, sealed.approveSQL(ctx, cfg, native))
	current, err := native.witness.Current(ctx, sealed.facts.Boundary.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, sealed.facts.Boundary, current)
	require.ErrorIs(t, native.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
}

func TestRecoveryActivationNativeTopologyRecipes(t *testing.T) {
	for _, recipe := range []string{"local-to-fleet", "fleet-to-local", "local-restore", "fleet-restore"} {
		t.Run(recipe, func(t *testing.T) {
			var cfg *config.Config
			var request RecoveryActivationRequest
			switch recipe {
			case "local-to-fleet":
				cfg, request = activationFleetFixture(t)
			case "fleet-to-local", "fleet-restore":
				cfg, request = activationFromActivatedFleet(t, recipe == "fleet-restore")
			case "local-restore":
				local, prepare := boundedActivationSourceFixture(t)
				cfg, request = activationPreparedFixture(t, local, prepare)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			result, err := ActivateRecovery(ctx, cfg, request)
			require.NoError(t, err)
			require.True(t, result.HistoricallyComplete)
			require.True(t, result.CurrentAdmissionValid)
			request.ExpectedDecisionSHA256 = result.DecisionSHA256
			body, err := os.ReadFile(filepath.Join(request.ActivationDirectory, "decision.json"))
			require.NoError(t, err)
			var record recoveryActivationRecord
			require.NoError(t, json.Unmarshal(body, &record))
			require.Equal(t, recipe, string(record.Preparation.Transfer.Direction))
			runActivationChild(t, cfg, request)
		})
	}
}

// This separate contract qualifies normal startup on an explicitly materialized historical baseline.
func TestRecoveryActivationBoundedSourceNormalStartup(t *testing.T) {
	cfg, prepare := boundedActivationSourceFixtureWithStartup(t, true)
	cfg, request := activationPreparedFixture(t, cfg, prepare)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	result, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, result.HistoricallyComplete)
	require.True(t, result.CurrentAdmissionValid)
	require.Equal(t, 3, result.CompletedPhases)
	request.ExpectedDecisionSHA256 = result.DecisionSHA256
	runActivationChild(t, cfg, request)
}

func TestRecoveryActivationFullEmbeddedApplication(t *testing.T) {
	valkey, postgres, endpoint := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if valkey == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: full application activation needs native shared owners")
	}
	cfg, publish, _ := runtimePublicationFixture(t, "completed-migration")
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	cfg, request := activationPreparedFixture(t, cfg, publish.PrepareRequest)
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Minute)
	defer cancel()
	result, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, result.HistoricallyComplete)
	require.True(t, result.CurrentAdmissionValid)
	require.Equal(t, 3, result.CompletedPhases)
}
