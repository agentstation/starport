package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRestoreLocalAccessRenewsCredentialWithoutOpeningAdmission(t *testing.T) {
	source, capture := backupApplicationFixture(t)
	sourceStore, err := localauth.NewStore(source.EffectivePaths().LocalTokenFile)
	require.NoError(t, err)
	old, err := sourceStore.Rotate(t.Context(), time.Now())
	require.NoError(t, err)
	oldCookie, _, err := localauth.IssueSession(old, localauth.GrantLocalToken, time.Now())
	require.NoError(t, err)
	_, err = CloseBackupBoundary(t.Context(), source)
	require.NoError(t, err)
	receipt, err := CaptureBackup(t.Context(), source, capture)
	require.NoError(t, err)
	parent := filepath.Join(t.TempDir(), "target")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_SECURITY_MASTER_KEY": source.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": source.EffectivePaths().DeploymentID,
	}).Load(t.Context())
	require.NoError(t, err)
	request := recovery.PrepareRequest{
		VerifyRequest:  recovery.VerifyRequest{Directory: receipt.Directory, ManifestSHA256: receipt.ManifestSHA256},
		Operation:      recovery.RestoreOperation{ID: "restore-local-access", FencingEvidence: "incident/all-writers-fenced"},
		FilesDirectory: filepath.Join(parent, "prepared"),
	}
	prepared, err := PrepareBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	require.NoFileExists(t, cfg.EffectivePaths().LocalTokenFile)
	var savedToken []byte
	for _, file := range prepared.FilePlan {
		if file.Role == "local-token" {
			require.Equal(t, "operator-credential", file.Action)
			savedToken, err = os.ReadFile(filepath.Join(request.FilesDirectory, "files", file.ArtifactID))
			require.NoError(t, err)
		}
	}
	require.Contains(t, string(savedToken), old.Secret)
	var output, stderr bytes.Buffer
	deps := starportcli.Dependencies{
		Stdin: strings.NewReader(""), Stdout: &output, Stderr: &stderr,
		ResolvePaths: func() (config.Paths, error) { return cfg.EffectivePaths(), nil },
		RunServer: func(context.Context, starportcli.GatewayOptions, starportcli.ServerOutput) error {
			t.Fatal("rotation started gateway")
			return nil
		},
	}
	deps.LoadConfig = func(context.Context) (*config.Config, error) { return cfg, nil }
	deps.StartDevelopment = func(context.Context, starportcli.GatewayOptions) (starportcli.DevelopmentSession, error) {
		t.Fatal("rotation started development")
		return starportcli.DevelopmentSession{}, nil
	}
	deps.Initialize = func(context.Context, starportcli.InitOptions) (starportcli.InitResult, error) {
		t.Fatal("rotation initialized gateway")
		return starportcli.InitResult{}, nil
	}
	deps.Diagnose = func(context.Context, diagnosis.Options) diagnosis.Report {
		t.Fatal("rotation started diagnostics")
		return diagnosis.Report{}
	}
	require.NoError(t, starportcli.Run(t.Context(), []string{"starport", "auth", "rotate", "--no-secret", "--json"}, deps))
	targetStore, err := localauth.NewStore(cfg.EffectivePaths().LocalTokenFile)
	require.NoError(t, err)
	current, err := targetStore.Peek(t.Context())
	require.NoError(t, err)
	require.False(t, current.Authorizes(old.Secret))
	_, err = localauth.VerifySession(oldCookie, current, time.Now())
	require.Error(t, err)
	require.NotContains(t, output.String(), old.Secret)
	require.NotContains(t, output.String(), current.Secret)
	require.Empty(t, stderr.String())
	repeated, err := PrepareBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	require.Equal(t, prepared, repeated)
	retained, err := targetStore.Peek(t.Context())
	require.NoError(t, err)
	require.Equal(t, current, retained)
	sourceRetained, err := sourceStore.Peek(t.Context())
	require.NoError(t, err)
	require.Equal(t, old, sourceRetained)
	_, err = storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	witness, err := recovery.New(db)
	require.NoError(t, err)
	boundary, err := witness.Current(t.Context(), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.False(t, boundary.Open)
	require.NoError(t, db.Close())
	_, err = blob.NewFilesystem(cfg.Files.Path)
	require.ErrorIs(t, err, blob.ErrImportRestricted)
}
