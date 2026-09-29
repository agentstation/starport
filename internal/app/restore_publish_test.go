package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/catalog"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func policyPublicationFixture(t *testing.T, wrongOwner bool) (*config.Config, recovery.PublishFilesRequest) {
	t.Helper()
	return policyPublicationRoleFixture(t, config.InferenceCredentialPolicyRole, wrongOwner)
}

func policyPublicationRoleFixture(t *testing.T, role string, wrongOwner bool) (*config.Config, recovery.PublishFilesRequest) {
	t.Helper()
	source, capture := backupApplicationFixture(t)
	paths := source.EffectivePaths()
	owner := credentials.SelectionPolicyOwner{Product: "starport", Deployment: paths.DeploymentID, Instance: paths.InstanceID}
	if wrongOwner {
		owner.Instance = "another-replica"
	}
	if role == config.AcquisitionPolicyRole {
		seedAcquisitionPolicy(t, source.CatalogCredentialPolicyDirectory(), owner)
	} else {
		store, err := credentials.OpenSelectionPolicyStore(t.Context(), source.InferenceCredentialPolicyDirectory(), owner, true)
		require.NoError(t, err)
		require.NoError(t, store.Accept(t.Context(), "openai"))
	}
	_, err := CloseBackupBoundary(t.Context(), source)
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
		FilesDirectory: filepath.Join(parent, "prepared"), Operation: recovery.RestoreOperation{ID: "restore-policy", FencingEvidence: "incident/fenced-writers"},
	}, Role: role}
}

func seedAcquisitionPolicy(t *testing.T, path string, owner credentials.SelectionPolicyOwner) {
	t.Helper()
	directory, err := productfiles.NewDirectory(path)
	require.NoError(t, err)
	for _, provider := range []string{"", "openai"} {
		name, policy := "policy.json", "starport-catalog-v1"
		if provider != "" {
			digest := sha256.Sum256([]byte(provider))
			name, policy = "provider-"+hex.EncodeToString(digest[:])+".json", "starport-catalog-v2"
		}
		body, err := json.Marshal(struct {
			Schema   int                              `json:"schema_version"`
			Owner    credentials.SelectionPolicyOwner `json:"owner"`
			Provider string                           `json:"provider,omitempty"`
			Policy   string                           `json:"policy"`
		}{1, owner, provider, policy})
		require.NoError(t, err)
		require.NoError(t, directory.CompareAndPublish(t.Context(), name, nil, body))
	}
}

func TestRestorePublishAcquisitionPolicyRetainsDefaultAndAcceptedProvider(t *testing.T) {
	cfg, request := policyPublicationRoleFixture(t, config.AcquisitionPolicyRole, false)
	first, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, first.Tree.Published)
	require.Equal(t, cfg.CatalogCredentialPolicyDirectory(), first.Tree.Destination)
	require.Len(t, first.Remaining, 1)
	require.Equal(t, "operator-credential", first.Remaining[0].Action)
	requirePublicationBarriers(t, cfg)
	require.NoDirExists(t, cfg.InferenceCredentialPolicyDirectory())
	settings := catalog.Settings{DeploymentID: cfg.EffectivePaths().DeploymentID, InstanceID: cfg.EffectivePaths().InstanceID}
	require.NoError(t, settings.InspectCredentialPolicy(t.Context(), first.Tree.Destination))
	body, err := os.ReadFile(filepath.Join(first.Tree.Destination, "policy.json"))
	require.NoError(t, err)
	require.Contains(t, string(body), "starport-catalog-v1")
	digest := sha256.Sum256([]byte("openai"))
	body, err = os.ReadFile(filepath.Join(first.Tree.Destination, "provider-"+hex.EncodeToString(digest[:])+".json"))
	require.NoError(t, err)
	require.Contains(t, string(body), "starport-catalog-v2")
	again, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, again.Tree.Reused)
	require.Equal(t, first.Tree.DirectoryIdentity, again.Tree.DirectoryIdentity)
	requirePublicationBarriers(t, cfg)
}

func TestRestorePublishAcquisitionPolicyRefusesDifferentOwner(t *testing.T) {
	cfg, request := policyPublicationRoleFixture(t, config.AcquisitionPolicyRole, true)
	_, err := PublishBackupFiles(t.Context(), cfg, request)
	require.Error(t, err)
	require.NoDirExists(t, cfg.CatalogCredentialPolicyDirectory())
	requirePublicationBarriers(t, cfg)
}

func requirePublicationBarriers(t *testing.T, cfg *config.Config) {
	t.Helper()
	_, err := storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
	_, err = blob.NewFilesystem(cfg.Files.Path)
	require.ErrorIs(t, err, blob.ErrImportRestricted)
	require.NoFileExists(t, cfg.EffectivePaths().LocalTokenFile)
}

func TestRestorePublishFilesCommandPreservesOwnerPolicyAndBarriers(t *testing.T) {
	cfg, request := policyPublicationFixture(t, false)
	deps := starportcli.Dependencies{Stdin: strings.NewReader(""), LoadConfig: func(context.Context) (*config.Config, error) { return cfg, nil }, PublishBackupFiles: PublishBackupFiles,
		RunServer: func(context.Context, starportcli.GatewayOptions) error {
			t.Fatal("publication started gateway")
			return nil
		},
	}
	deps.StartDevelopment = func(context.Context, starportcli.GatewayOptions) (starportcli.DevelopmentSession, error) {
		t.Fatal("publication started development")
		return starportcli.DevelopmentSession{}, nil
	}
	deps.Initialize = func(context.Context, starportcli.InitOptions) (starportcli.InitResult, error) {
		t.Fatal("publication initialized a gateway")
		return starportcli.InitResult{}, nil
	}
	deps.ResolvePaths = func() (config.Paths, error) { return cfg.EffectivePaths(), nil }
	deps.Diagnose = func(context.Context, diagnosis.Options) diagnosis.Report {
		t.Fatal("publication started diagnostics")
		return diagnosis.Report{}
	}
	var output, stderr bytes.Buffer
	deps.Stdout, deps.Stderr = &output, &stderr
	args := []string{"starport", "backup", "publish-files", "--directory", request.Directory, "--manifest-sha256", request.ManifestSHA256, "--files-directory", request.FilesDirectory, "--operation", request.Operation.ID, "--fencing-evidence", request.Operation.FencingEvidence, "--role", request.Role, "--json"}
	require.NoError(t, starportcli.Run(t.Context(), args, deps))
	var first recovery.PublishFilesResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &first))
	require.True(t, first.Tree.Published)
	require.False(t, first.Preparation.Prepared.Boundary.Open)
	require.Equal(t, cfg.InferenceCredentialPolicyDirectory(), first.Tree.Destination)
	require.Len(t, first.Remaining, 1)
	require.Equal(t, "operator-credential", first.Remaining[0].Action)
	require.NotContains(t, output.String(), cfg.Security.MasterKey)
	requirePublicationBarriers(t, cfg)
	owner := credentials.SelectionPolicyOwner{Product: "starport", Deployment: cfg.EffectivePaths().DeploymentID, Instance: cfg.EffectivePaths().InstanceID}
	restored, err := credentials.OpenSelectionPolicyStore(t.Context(), first.Tree.Destination, owner, false)
	require.NoError(t, err)
	accepted, err := restored.Policy(t.Context(), "openai")
	require.NoError(t, err)
	require.Equal(t, credentials.InferencePolicyCurrent, accepted)
	legacy, err := restored.Policy(t.Context(), "anthropic")
	require.NoError(t, err)
	require.Equal(t, credentials.InferencePolicyLegacy, legacy)
	output.Reset()
	require.NoError(t, starportcli.Run(t.Context(), args, deps))
	var again recovery.PublishFilesResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &again))
	require.True(t, again.Tree.Reused)
	require.Equal(t, first.Tree.DirectoryIdentity, again.Tree.DirectoryIdentity)
	requirePublicationBarriers(t, cfg)
	require.Empty(t, stderr.String())
}

func TestRestorePublishFilesRefusesBeforeTargetCreation(t *testing.T) {
	for _, mode := range []string{"wrong-digest", "wrong-key", "unsupported-role", "different-replica", "overlapping-target", "missing-policy", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request := policyPublicationFixture(t, false)
			ctx := t.Context()
			switch mode {
			case "wrong-digest":
				request.ManifestSHA256 = strings.Repeat("0", 64)
			case "wrong-key":
				cfg.Security.MasterKey = strings.Repeat("x", 32)
			case "unsupported-role":
				request.Role = "local-token"
			case "different-replica":
				other, err := config.NewLoader().WithPaths(cfg.EffectivePaths()).WithEnvFiles().WithEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": cfg.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": cfg.EffectivePaths().DeploymentID, "STARPORT_INSTANCE_ID": "another-replica"}).Load(t.Context())
				require.NoError(t, err)
				cfg = other
			case "overlapping-target":
				request.FilesDirectory = cfg.InferenceCredentialPolicyDirectory()
			case "missing-policy":
				base, prepare := restoreApplicationFixture(t)
				cfg = base
				request.PrepareRequest = prepare
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := PublishBackupFiles(ctx, cfg, request)
			require.Error(t, err)
			require.NoDirExists(t, cfg.InferenceCredentialPolicyDirectory())
			require.NoDirExists(t, cfg.Storage.Badger.Path)
			require.NoFileExists(t, cfg.Storage.SQL.SQLite.Path)
		})
	}
}

func TestRestorePublishFilesPreservesConflictingPolicy(t *testing.T) {
	cfg, request := policyPublicationFixture(t, false)
	parent, err := productfiles.NewDirectory(cfg.InferenceCredentialPolicyDirectory())
	require.NoError(t, err)
	require.NoError(t, parent.CompareAndPublish(t.Context(), "operator-owned", nil, []byte("preserve")))
	result, err := PublishBackupFiles(t.Context(), cfg, request)
	require.Error(t, err)
	require.False(t, result.Tree.Published)
	body, err := os.ReadFile(filepath.Join(cfg.InferenceCredentialPolicyDirectory(), "operator-owned"))
	require.NoError(t, err)
	require.Equal(t, "preserve", string(body))
	requirePublicationBarriers(t, cfg)
}

func TestRestorePublishFilesRejectsInvalidOwnerAndChangedOperation(t *testing.T) {
	t.Run("owner", func(t *testing.T) {
		cfg, request := policyPublicationFixture(t, true)
		_, err := PublishBackupFiles(t.Context(), cfg, request)
		require.Error(t, err)
		require.NoDirExists(t, cfg.InferenceCredentialPolicyDirectory())
		requirePublicationBarriers(t, cfg)
	})
	t.Run("operation", func(t *testing.T) {
		cfg, request := policyPublicationFixture(t, false)
		_, err := PrepareBackup(t.Context(), cfg, request.PrepareRequest)
		require.NoError(t, err)
		request.Operation.ID = "different-operation"
		_, err = PublishBackupFiles(t.Context(), cfg, request)
		require.Error(t, err)
		require.NoDirExists(t, cfg.InferenceCredentialPolicyDirectory())
		requirePublicationBarriers(t, cfg)
	})
}

func TestRestorePublishFilesSharedRecipe(t *testing.T) {
	for _, role := range []string{config.InferenceCredentialPolicyRole, config.AcquisitionPolicyRole} {
		t.Run(role, func(t *testing.T) { testRestorePublishSharedRole(t, role) })
	}
}

func testRestorePublishSharedRole(t *testing.T, role string) {
	t.Helper()
	valkey, postgres, endpoint := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if valkey == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: shared publication requires Valkey, PostgreSQL, and object storage")
	}
	cfg, request := policyPublicationRoleFixture(t, role, false)
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	first, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, first.Tree.Published)
	again, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, again.Tree.Reused)
	require.Equal(t, first.Tree.DirectoryIdentity, again.Tree.DirectoryIdentity)
	require.False(t, again.Preparation.Prepared.Boundary.Open)
	_, err = storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
	blobs, err := openBlob(t.Context(), cfg.Files)
	require.NoError(t, err)
	_, err = blobs.Get(t.Context(), "retained")
	require.ErrorIs(t, err, blob.ErrImportRestricted)
	require.NoFileExists(t, cfg.EffectivePaths().LocalTokenFile)
	kv, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	keys, err := kv.ScanWithPrefix(t.Context(), "", 1000)
	require.NoError(t, err)
	require.NoError(t, kv.BatchDelete(t.Context(), keys))
	require.NoError(t, kv.Close())
}
