package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func backupApplicationFixture(t *testing.T) (*config.Config, recovery.CaptureRequest) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "private")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("k", 32), "STARPORT_DEPLOYMENT_ID": "backup-test",
	}).Load(t.Context())
	require.NoError(t, err)
	for _, directory := range []string{cfg.Storage.Badger.Path, filepath.Dir(cfg.Storage.SQL.SQLite.Path), cfg.Files.Path, filepath.Dir(cfg.EffectivePaths().LocalTokenFile)} {
		require.NoError(t, os.MkdirAll(filepath.Dir(directory), 0o700))
		require.NoError(t, os.MkdirAll(directory, 0o700))
		_, err = productfiles.ExistingDirectory(directory)
		require.NoError(t, err)
	}
	kv, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	require.NoError(t, kv.Set(t.Context(), "account:retained", []byte("retained account")))
	require.NoError(t, kv.Close())
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(t.Context()))
	require.NoError(t, db.Close())
	blobs, err := blob.NewFilesystem(cfg.Files.Path)
	require.NoError(t, err)
	_, err = blobs.Put(t.Context(), "retained", strings.NewReader("retained bytes"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfg.EffectivePaths().LocalTokenFile, []byte("private local token"), 0o600))
	return cfg, recovery.CaptureRequest{Destination: filepath.Join(parent, "backup"), Build: "test", OperationID: "capture-one", FencingEvidence: "operator-stopped-all-writers", KeyReference: "test-master-key"}
}

func TestBackupApplicationCapturesExistingDeployment(t *testing.T) {
	cfg, request := backupApplicationFixture(t)
	_, err := CaptureBackup(t.Context(), cfg, request)
	require.ErrorIs(t, err, recovery.ErrClosed)
	require.NoDirExists(t, request.Destination)
	closed, err := CloseBackupBoundary(t.Context(), cfg)
	require.NoError(t, err)
	require.False(t, closed.Open)
	again, err := CloseBackupBoundary(t.Context(), cfg)
	require.NoError(t, err)
	require.Equal(t, closed, again)
	result, err := CaptureBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	require.Equal(t, closed.Epoch, result.RecoveryEpoch)
	body, err := os.ReadFile(filepath.Join(request.Destination, "files", "inventory.json"))
	require.NoError(t, err)
	var inventory config.BackupInventory
	require.NoError(t, json.Unmarshal(body, &inventory))
	require.Len(t, inventory.Files, 1)
	require.Equal(t, "local-token", inventory.Files[0].Role)
	preserved, err := os.ReadFile(filepath.Join(request.Destination, "files", inventory.Files[0].ArtifactID))
	require.NoError(t, err)
	require.Equal(t, "private local token", string(preserved))
	// Verification must not open, repair, or recreate any live store.
	require.NoError(t, os.RemoveAll(cfg.Storage.Badger.Path))
	require.NoError(t, os.Remove(cfg.Storage.SQL.SQLite.Path))
	verified, err := VerifyBackup(t.Context(), cfg, recovery.VerifyRequest{Directory: result.Directory, ManifestSHA256: result.ManifestSHA256})
	require.NoError(t, err)
	require.Equal(t, result, verified)
	require.NoDirExists(t, cfg.Storage.Badger.Path)
	require.NoFileExists(t, cfg.Storage.SQL.SQLite.Path)
	cfg.Security.MasterKey = strings.Repeat("x", 32)
	_, err = VerifyBackup(t.Context(), cfg, recovery.VerifyRequest{Directory: result.Directory, ManifestSHA256: result.ManifestSHA256})
	require.Error(t, err)
}

func TestBackupApplicationRequiresClosedApproval(t *testing.T) {
	cfg, request := backupApplicationFixture(t)
	closed, err := CloseBackupBoundary(t.Context(), cfg)
	require.NoError(t, err)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	witness, err := recovery.New(db)
	require.NoError(t, err)
	opened, err := witness.Approve(t.Context(), closed, "backend", "independent-history")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	_, err = CaptureBackup(t.Context(), cfg, request)
	require.ErrorIs(t, err, recovery.ErrClosed)
	require.NoDirExists(t, request.Destination)
	closed, err = CloseBackupBoundary(t.Context(), cfg)
	require.NoError(t, err)
	require.False(t, closed.Open)
	require.Equal(t, opened.Epoch+1, closed.Epoch)
	_, err = CaptureBackup(t.Context(), cfg, request)
	require.NoError(t, err)
}

func TestBackupApplicationRefusesConfigurationChangedAfterLoad(t *testing.T) {
	cfg, request := backupApplicationFixture(t)
	name := cfg.EffectivePaths().ConfigFile
	require.NoError(t, os.WriteFile(name, []byte("STARPORT_LOG_LEVEL=info\n"), 0o600))
	loaded, err := config.NewLoader().WithPaths(cfg.EffectivePaths()).WithEnvironment(map[string]string{
		"STARPORT_SECURITY_MASTER_KEY": cfg.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": cfg.EffectivePaths().DeploymentID,
	}).Load(t.Context())
	require.NoError(t, err)
	_, err = CloseBackupBoundary(t.Context(), loaded)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(name, []byte("STARPORT_LOG_LEVEL=debug\n"), 0o600))
	_, err = CaptureBackup(t.Context(), loaded, request)
	require.ErrorContains(t, err, "configuration changed after selection")
	require.NoFileExists(t, filepath.Join(request.Destination, "manifest.json"))
}

func TestBackupCommandsUseNativeApplicationCapture(t *testing.T) {
	cfg, request := backupApplicationFixture(t)
	var output, stderr bytes.Buffer
	deps := starportcli.Dependencies{
		Stdin: strings.NewReader(""), Stdout: &output, Stderr: &stderr,
		Build:               starportcli.BuildInfo{Version: request.Build},
		LoadConfig:          func(context.Context) (*config.Config, error) { return cfg, nil },
		ResolvePaths:        func() (config.Paths, error) { return cfg.EffectivePaths(), nil },
		CloseBackupBoundary: CloseBackupBoundary, CaptureBackup: CaptureBackup, VerifyBackup: VerifyBackup,
		RunServer: func(context.Context, starportcli.GatewayOptions) error { panic("backup started gateway") },
		StartDevelopment: func(context.Context, starportcli.GatewayOptions) (starportcli.DevelopmentSession, error) {
			panic("backup started development")
		},
		Initialize: func(context.Context, starportcli.InitOptions) (starportcli.InitResult, error) {
			panic("backup initialized gateway")
		},
		Diagnose: func(context.Context, diagnosis.Options) diagnosis.Report { panic("backup ran diagnostics") },
	}
	require.NoError(t, starportcli.Run(t.Context(), []string{"starport", "backup", "close"}, deps))
	require.Contains(t, output.String(), "does not stop processes")
	output.Reset()
	require.NoError(t, starportcli.Run(t.Context(), []string{"starport", "backup", "create", "--destination", request.Destination, "--operation", request.OperationID, "--fencing-evidence", request.FencingEvidence, "--key-reference", request.KeyReference, "--json"}, deps))
	var result recovery.CaptureResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.NotContains(t, output.String(), cfg.Security.MasterKey)
	require.NotContains(t, output.String(), request.KeyReference)
	output.Reset()
	require.NoError(t, starportcli.Run(t.Context(), []string{"starport", "backup", "verify", "--directory", result.Directory, "--manifest-sha256", result.ManifestSHA256}, deps))
	require.Contains(t, output.String(), "does not approve recovery")
	require.Empty(t, stderr.String())
}
