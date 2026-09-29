package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"net/url"
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
	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func restoreApplicationFixture(t *testing.T) (*config.Config, recovery.PrepareRequest) {
	t.Helper()
	source, capture := backupApplicationFixture(t)
	_, err := CloseBackupBoundary(t.Context(), source)
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
	request := recovery.PrepareRequest{VerifyRequest: recovery.VerifyRequest{Directory: receipt.Directory, ManifestSHA256: receipt.ManifestSHA256}, Operation: recovery.RestoreOperation{ID: "restore-one", FencingEvidence: "incident/writers-fenced"}, FilesDirectory: filepath.Join(parent, "prepared")}
	return cfg, request
}

func TestRestoreCommandsPrepareRestrictedNativeStores(t *testing.T) {
	cfg, request := restoreApplicationFixture(t)
	var output, stderr bytes.Buffer
	deps := starportcli.Dependencies{
		Stdin: strings.NewReader(""), Stdout: &output, Stderr: &stderr,
		LoadConfig:    func(context.Context) (*config.Config, error) { return cfg, nil },
		PrepareBackup: PrepareBackup,
		RunServer:     func(context.Context, starportcli.GatewayOptions) error { panic("restore started gateway") },
	}
	deps.StartDevelopment = func(context.Context, starportcli.GatewayOptions) (starportcli.DevelopmentSession, error) {
		panic("restore started development")
	}
	deps.Initialize = func(context.Context, starportcli.InitOptions) (starportcli.InitResult, error) {
		panic("restore initialized gateway")
	}
	deps.ResolvePaths = func() (config.Paths, error) { return cfg.EffectivePaths(), nil }
	deps.Diagnose = func(context.Context, diagnosis.Options) diagnosis.Report { panic("restore started diagnostics") }

	args := []string{"starport", "backup", "prepare", "--directory", request.Directory, "--manifest-sha256", request.ManifestSHA256, "--files-directory", request.FilesDirectory, "--operation", request.Operation.ID, "--fencing-evidence", request.Operation.FencingEvidence, "--json"}
	require.NoError(t, starportcli.Run(t.Context(), args, deps))
	var prepared recovery.PrepareResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &prepared))
	require.False(t, prepared.Prepared.Boundary.Open)
	require.Equal(t, request.Operation.FencingEvidence, prepared.Prepared.FencingEvidence)
	require.Equal(t, request.FilesDirectory, prepared.FilesDirectory)
	require.Len(t, prepared.FilePlan, 1)
	require.Equal(t, "local-token", prepared.FilePlan[0].Role)
	require.Equal(t, "operator-credential", prepared.FilePlan[0].Action)
	require.Equal(t, cfg.EffectivePaths().LocalTokenFile, prepared.FilePlan[0].Destination)
	require.NotContains(t, output.String(), cfg.Security.MasterKey)
	require.NoFileExists(t, cfg.EffectivePaths().LocalTokenFile, "the saved token must remain inactive")
	require.FileExists(t, filepath.Join(request.FilesDirectory, "files/inventory.json"))
	_, err := storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
	_, err = blob.NewFilesystem(cfg.Files.Path)
	require.ErrorIs(t, err, blob.ErrImportRestricted)
	output.Reset()
	require.NoError(t, starportcli.Run(t.Context(), args, deps))
	var repeated recovery.PrepareResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &repeated))
	require.Equal(t, prepared, repeated)
	request.Operation.FencingEvidence = "different-evidence"
	_, err = PrepareBackup(t.Context(), cfg, request)
	require.Error(t, err)
	require.Empty(t, stderr.String())
}

func TestRestoreApplicationRefusesBeforeTargetCreation(t *testing.T) {
	for _, mode := range []string{"wrong-digest", "wrong-key", "wrong-deployment", "target-in-backup", "overlapping-targets", "missing-evidence"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request := restoreApplicationFixture(t)
			original := cfg.Storage.Badger.Path
			switch mode {
			case "wrong-digest":
				request.ManifestSHA256 = strings.Repeat("0", 64)
			case "wrong-key":
				cfg.Security.MasterKey = strings.Repeat("x", 32)
			case "wrong-deployment":
				other, err := config.NewLoader().WithPaths(cfg.EffectivePaths()).WithEnvFiles().WithEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": cfg.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": "another"}).Load(t.Context())
				require.NoError(t, err)
				cfg = other
			case "target-in-backup":
				cfg.Storage.Badger.Path = filepath.Join(request.Directory, "unexpected")
			case "overlapping-targets":
				cfg.Files.Path = cfg.Storage.Badger.Path
			case "missing-evidence":
				request.Operation.FencingEvidence = ""
			}
			_, err := PrepareBackup(t.Context(), cfg, request)
			require.Error(t, err)
			require.NoDirExists(t, original)
			require.NoFileExists(t, cfg.Storage.SQL.SQLite.Path)
			require.NoDirExists(t, request.FilesDirectory)
			require.NoDirExists(t, filepath.Join(request.Directory, "unexpected"))
		})
	}
}

func TestRestoreApplicationPreservesPopulatedTargets(t *testing.T) {
	cfg, request := restoreApplicationFixture(t)
	_, err := productfiles.NewDirectory(cfg.Storage.Badger.Path)
	require.NoError(t, err)
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	require.NoError(t, store.Set(t.Context(), "operator-record", []byte("preserve")))
	require.NoError(t, store.Close())
	_, err = PrepareBackup(t.Context(), cfg, request)
	require.Error(t, err)
	store, err = storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	retained, err := store.Get(t.Context(), "operator-record")
	require.NoError(t, err)
	require.Equal(t, "preserve", string(retained))
	require.NoError(t, store.Close())
	require.NoDirExists(t, request.FilesDirectory)
	// SQL has already imported restricted state. An error is not a rollback claim.
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
}

func TestRestoreApplicationSharedRecipe(t *testing.T) {
	valkey, postgres, endpoint := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if valkey == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: shared restore requires Valkey, PostgreSQL, and object storage")
	}
	cfg, request := restoreApplicationFixture(t)
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	first, err := PrepareBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	again, err := PrepareBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.False(t, again.Prepared.Boundary.Open)
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
	kv, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	keys, err := kv.ScanWithPrefix(t.Context(), "", 1000)
	require.NoError(t, err)
	require.NoError(t, kv.BatchDelete(t.Context(), keys))
	require.NoError(t, kv.Close())
}

func configureSharedRestore(t *testing.T, cfg *config.Config, valkey, postgres, endpoint string) {
	t.Helper()
	cfg.Storage.Mode = storage.StorageTypeValkey
	cfg.Storage.Valkey.URL = valkey
	cfg.Storage.Valkey.AllowInsecure = true
	cfg.Storage.SQL.Mode = sqlstore.TypePostgres
	admin, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: postgres}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schema := "restore_" + strings.ToLower(rand.Text())
	quoted := pgx.Identifier{schema}.Sanitize()
	_, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+quoted)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		require.NoError(t, err)
	})
	parsed, err := url.Parse(postgres)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	cfg.Storage.SQL.Postgres.URL = parsed.String()
	cfg.Files.Backend = config.BlobBackendObjectStore
	bucket := "restore-" + strings.ToLower(rand.Text())
	cfg.Files.ObjectStore = config.ObjectStoreConfig{Bucket: bucket, Region: "us-east-1", Endpoint: endpoint, AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"}
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: awscredentials.NewStaticCredentialsProvider("starport-test", "starport-local-test-only", "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	_, err = client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
}
