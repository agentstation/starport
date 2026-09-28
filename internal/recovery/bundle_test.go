package recovery

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
)

func backupBundleFixture(t *testing.T) (BundleSources, BundleRequest, string) {
	t.Helper()
	return backupBundleRecipe(t, false)
}

func backupBundleRecipe(t *testing.T, shared bool) (BundleSources, BundleRequest, string) {
	t.Helper()
	kind := storage.StorageTypeBadger
	sqlConfig := sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(privateKVDirectory(t), "source.db")}}
	if shared {
		if os.Getenv("TEST_POSTGRES_URL") == "" || os.Getenv("TEST_BLOB_S3_ENDPOINT") == "" {
			t.Skip("UNVERIFIED: shared backup requires PostgreSQL and object storage fixtures")
		}
		kind = storage.StorageTypeValkey
		sqlConfig = isolatedWitnessPostgres(t, sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: os.Getenv("TEST_POSTGRES_URL")}})
	}
	kv, transfer, _ := kvTransferStores(t, kind)
	require.NoError(t, kv.Set(t.Context(), "account:one", []byte("account record")))
	db, err := sqlstore.Open(sqlConfig)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	witness, err := New(db)
	require.NoError(t, err)
	boundary, err := witness.Initialize(t.Context(), "deployment")
	require.NoError(t, err)
	var blobs blob.Store
	if shared {
		blobs = bundleObjectFixture(t)
	} else {
		blobs, err = blob.NewFilesystem(filepath.Join(privateKVDirectory(t), "blobs"))
		require.NoError(t, err)
	}
	_, err = blobs.Publish(t.Context(), "file-one", strings.NewReader("linked bytes"))
	require.NoError(t, err)
	require.NoError(t, blobs.Retire(t.Context(), "old-file"))
	encryption, err := credentials.NewEncryptionService([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	configFile := filepath.Join(privateKVDirectory(t), "config.env")
	require.NoError(t, os.WriteFile(configFile, []byte("STARPORT_CATALOG_SOURCE=embedded\n"), 0o600))
	source := BundleSources{KV: transfer, SQL: db, Blobs: blobs, Encryption: encryption, Files: []BundleFile{{ID: "configuration/config.env", Path: configFile}}}
	request := BundleRequest{OperationID: "backup-1", Build: "qualification", Boundary: boundary, FencingEvidence: "test/all-fixture-writers-owned", KeyReference: "test/master", ExternalRequirements: []string{"provider credentials supplied by the operator"}}
	return source, request, filepath.Join(privateKVDirectory(t), "backup")
}

func TestBackupBundleBindsStoresFilesAndKeyAccess(t *testing.T) {
	source, request, destination := backupBundleFixture(t)
	manifest, err := BackupBundle(t.Context(), destination, source, request)
	require.NoError(t, err)
	require.Len(t, manifest.Artifacts, 6)
	require.Equal(t, int64(1), manifest.KV.Records)
	require.Equal(t, int64(2), manifest.Blobs.Objects)
	require.Equal(t, int64(1), manifest.Blobs.Retired)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := VerifyBundle(t.Context(), destination, digest, source.Encryption)
	require.NoError(t, err)
	require.Equal(t, manifest, verified)
	wrong, err := credentials.NewEncryptionService([]byte(strings.Repeat("x", 32)))
	require.NoError(t, err)
	_, err = VerifyBundle(t.Context(), destination, digest, wrong)
	require.ErrorContains(t, err, "encryption-key access")
	_, err = VerifyBundle(t.Context(), destination, strings.Repeat("0", 64), source.Encryption)
	require.ErrorContains(t, err, "independently retained digest")
	_, err = BackupBundle(t.Context(), destination, source, request)
	require.Error(t, err)
	_, err = VerifyBundle(t.Context(), destination, digest, source.Encryption)
	require.NoError(t, err)
	witness, err := New(source.SQL)
	require.NoError(t, err)
	current, err := witness.Current(t.Context(), request.Boundary.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, request.Boundary, current, "backup must never approve admission")
}

func TestBackupBundleDetectsChangedArtifacts(t *testing.T) {
	for _, kind := range []string{"missing-sql", "changed-kv", "extra-file", "changed-manifest", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			manifest, err := BackupBundle(t.Context(), destination, source, request)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			switch kind {
			case "missing-sql":
				require.NoError(t, os.Remove(filepath.Join(destination, "sql/starport.db")))
			case "changed-kv":
				require.NoError(t, os.WriteFile(filepath.Join(destination, "kv/kv.db"), []byte("changed"), 0o600))
			case "extra-file":
				require.NoError(t, os.WriteFile(filepath.Join(destination, "extra"), []byte("unlisted"), 0o600))
			case "changed-manifest":
				manifest.Request.Boundary.Epoch++
				body, err := json.Marshal(manifest)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(destination, bundleManifestFile), body, 0o600))
			case "symlink":
				target := filepath.Join(destination, "files/configuration/config.env")
				require.NoError(t, os.Remove(target))
				require.NoError(t, os.Symlink(source.Files[0].Path, target))
			}
			_, err = VerifyBundle(t.Context(), destination, digest, source.Encryption)
			require.Error(t, err)
		})
	}
}

type boundaryChangingTransfer struct {
	storage.RecordSource
	before func() error
}

func (s boundaryChangingTransfer) Enumerate(ctx context.Context, yield func(storage.TransferRecord) error) error {
	if err := s.before(); err != nil {
		return err
	}
	return s.RecordSource.Enumerate(ctx, yield)
}

func TestBackupBundleLeavesNoManifestAfterBoundaryChange(t *testing.T) {
	source, request, destination := backupBundleFixture(t)
	witness, err := New(source.SQL)
	require.NoError(t, err)
	source.KV = boundaryChangingTransfer{RecordSource: source.KV, before: func() error {
		_, err := witness.Close(t.Context(), request.Boundary)
		return err
	}}
	_, err = BackupBundle(t.Context(), destination, source, request)
	require.ErrorIs(t, err, ErrConflict)
	_, err = os.Stat(filepath.Join(destination, bundleManifestFile))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.DirExists(t, filepath.Join(destination, "kv"))
}

func TestBackupBundleRefusesIncompleteInputs(t *testing.T) {
	for _, kind := range []string{"open-gate", "missing-key", "missing-file", "duplicate-id", "invalid-id", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			ctx := t.Context()
			switch kind {
			case "open-gate":
				request.Boundary.Open = true
			case "missing-key":
				source.Encryption = nil
			case "missing-file":
				source.Files[0].Path += "-missing"
			case "duplicate-id":
				source.Files = append(source.Files, source.Files[0])
			case "invalid-id":
				source.Files[0].ID = "../../outside"
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := BackupBundle(ctx, destination, source, request)
			require.Error(t, err)
			_, err = os.Stat(filepath.Join(destination, bundleManifestFile))
			require.True(t, errors.Is(err, os.ErrNotExist))
		})
	}
}

func bundleObjectFixture(t *testing.T) blob.Store {
	t.Helper()
	endpoint := os.Getenv("TEST_BLOB_S3_ENDPOINT")
	bucket := "bundle-" + strings.ToLower(rand.Text())
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: awscredentials.NewStaticCredentialsProvider("starport-test", "starport-local-test-only", "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	_, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	_, err = client.PutBucketVersioning(t.Context(), &s3.PutBucketVersioningInput{Bucket: aws.String(bucket), VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled}})
	require.NoError(t, err)
	store, err := blob.NewObjectStore(t.Context(), blob.ObjectStoreOptions{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"})
	require.NoError(t, err)
	return store
}

func TestBackupBundleSharedRecipe(t *testing.T) {
	source, request, destination := backupBundleRecipe(t, true)
	manifest, err := BackupBundle(t.Context(), destination, source, request)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := VerifyBundle(t.Context(), destination, digest, source.Encryption)
	require.NoError(t, err)
	require.Equal(t, manifest, verified)
	require.Equal(t, int64(1), verified.KV.Records)
	require.Equal(t, int64(2), verified.Blobs.Objects)
	require.Equal(t, int64(1), verified.Blobs.Retired)
}

func TestBackupBundleRejectsChangedSelectedConfiguration(t *testing.T) {
	source, request, destination := backupBundleFixture(t)
	source.Files[0].ExpectedSHA256 = strings.Repeat("0", 64)
	_, err := BackupBundle(t.Context(), destination, source, request)
	require.ErrorContains(t, err, "configuration changed after selection")
	_, err = os.Stat(filepath.Join(destination, bundleManifestFile))
	require.ErrorIs(t, err, os.ErrNotExist)
}
