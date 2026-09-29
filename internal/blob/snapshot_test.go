package blob

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
)

func snapshotDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Chmod(directory, 0o700))
	return directory
}

func snapshotObjects(t *testing.T) *ObjectStore {
	t.Helper()
	endpoint := os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_BLOB_S3_ENDPOINT is not configured")
	}
	store, err := NewObjectStore(t.Context(), ObjectStoreOptions{Endpoint: endpoint, Region: "us-east-1", Bucket: "snapshot-" + strings.ToLower(rand.Text()), Prefix: "deployment", AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"})
	require.NoError(t, err)
	_, err = store.client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(store.bucket)})
	require.NoError(t, err)
	_, err = store.client.PutBucketVersioning(t.Context(), &s3.PutBucketVersioningInput{Bucket: aws.String(store.bucket), VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled}})
	require.NoError(t, err)
	t.Logf("versioned snapshot fixture: %s", store.bucket)
	return store
}

func seedSnapshot(t *testing.T, store Store) {
	t.Helper()
	_, err := store.Put(t.Context(), "mutable", strings.NewReader("mutable bytes"))
	require.NoError(t, err)
	publications := store.(PublicationStore)
	_, err = publications.Publish(t.Context(), "live", strings.NewReader(strings.Repeat("payload", 900000)))
	require.NoError(t, err)
	_, err = publications.Publish(t.Context(), "empty", strings.NewReader(""))
	require.NoError(t, err)
	require.NoError(t, publications.Retire(t.Context(), "retired-without-record"))
}

func checkSnapshot(t *testing.T, store Store) {
	t.Helper()
	reader, err := store.Get(t.Context(), "mutable")
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "mutable bytes", string(data))
	publications := store.(PublicationStore)
	for key, payload := range map[string]string{"live": strings.Repeat("payload", 900000), "empty": ""} {
		reader, err := publications.ReadPublished(t.Context(), key)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, payload, string(data))
		_, err = publications.Publish(t.Context(), key, strings.NewReader("replacement"))
		require.ErrorIs(t, err, ErrPublicationExists)
	}
	_, err = publications.ReadPublished(t.Context(), "retired-without-record")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = publications.Publish(t.Context(), "retired-without-record", strings.NewReader("late writer"))
	require.ErrorIs(t, err, ErrPublicationExists)
}

// These fixtures remove barriers only to inspect adapter contracts. Full
// deployment approval must also prove fencing and independent recovery history.
func activateSnapshotFixture(t *testing.T, store Store) Store {
	t.Helper()
	switch target := store.(type) {
	case *Filesystem:
		_, err := NewFilesystem(target.root)
		require.ErrorIs(t, err, ErrImportRestricted)
		require.NoError(t, os.Remove(filepath.Join(target.root, filepath.FromSlash(blobImportKey))))
		reopened, err := NewFilesystem(target.root)
		require.NoError(t, err)
		return reopened
	case *ObjectStore:
		require.ErrorIs(t, target.probeLayout(t.Context()), ErrImportRestricted)
		_, err := target.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(target.bucket), Key: aws.String(target.objectKey(blobLayoutKey)), Body: strings.NewReader(blobLayoutVersion), IfNoneMatch: aws.String("*")})
		require.NoError(t, err)
		_, err = target.client.DeleteObject(t.Context(), &s3.DeleteObjectInput{Bucket: aws.String(target.bucket), Key: aws.String(target.objectKey(blobImportKey))})
		require.NoError(t, err)
		return target
	default:
		t.Fatal("unsupported fixture")
		return nil
	}
}

func TestBlobSnapshotTransfer(t *testing.T) {
	for _, sourceKind := range []string{"filesystem", "objectstore"} {
		t.Run(sourceKind, func(t *testing.T) {
			var source Store
			if sourceKind == "filesystem" {
				filesystem, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
				require.NoError(t, err)
				source = filesystem
			} else {
				source = snapshotObjects(t)
			}
			seedSnapshot(t, source)
			archive := filepath.Join(snapshotDirectory(t), "blobs.tar")
			receipt, err := Backup(t.Context(), source, archive)
			require.NoError(t, err)
			require.Equal(t, int64(4), receipt.Objects)
			require.Equal(t, int64(1), receipt.Retired)
			for _, targetKind := range []string{"filesystem", "objectstore"} {
				t.Run(targetKind, func(t *testing.T) {
					var target Store
					if targetKind == "filesystem" {
						destination := filepath.Join(snapshotDirectory(t), "restored")
						result, err := RestoreFilesystem(t.Context(), destination, archive, "recovery-1", receipt)
						require.NoError(t, err)
						require.True(t, result.Published)
						target = &Filesystem{root: destination}
						result, err = RestoreFilesystem(t.Context(), destination, archive, "recovery-1", receipt)
						require.Error(t, err)
						require.False(t, result.Published)
					} else {
						objects := snapshotObjects(t)
						scratch := snapshotDirectory(t)
						require.NoError(t, RestoreObjectStore(t.Context(), objects, archive, scratch, "recovery-1", receipt))
						require.NoError(t, RestoreObjectStore(t.Context(), objects, archive, scratch, "recovery-1", receipt))
						require.Error(t, RestoreObjectStore(t.Context(), objects, archive, scratch, "different-owner", receipt))
						target = objects
					}
					checkSnapshot(t, activateSnapshotFixture(t, target))
					checkSnapshot(t, source)
				})
			}
		})
	}
}

func TestBlobSnapshotRejectsInvalidArchiveBeforePublication(t *testing.T) {
	for _, kind := range []string{"digest", "traversal", "duplicate", "symlink", "bad-envelope", "extra-trailer", "wrong-count"} {
		t.Run(kind, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := tar.NewWriter(&buffer)
			address, payload, entryType := blobAddress(objectsDir, "object"), "payload", byte(tar.TypeReg)
			switch kind {
			case "traversal":
				address = "../outside"
			case "symlink":
				entryType, payload = tar.TypeSymlink, ""
			case "bad-envelope":
				address = blobAddress(retainedDir, "object")
			}
			header := &tar.Header{Name: address, Mode: 0o600, Size: int64(len(payload)), Typeflag: entryType, Format: tar.FormatUSTAR}
			if kind == "symlink" {
				header.Linkname = "../../outside"
			}
			require.NoError(t, writer.WriteHeader(header))
			_, err := writer.Write([]byte(payload))
			require.NoError(t, err)
			if kind == "duplicate" {
				require.NoError(t, writer.WriteHeader(header))
				_, err = writer.Write([]byte(payload))
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			if kind == "extra-trailer" {
				_, err = buffer.Write(make([]byte, 512))
				require.NoError(t, err)
			}
			data := buffer.Bytes()
			digest := sha256.Sum256(data)
			receipt := Snapshot{Format: blobSnapshotFormat, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Objects: 1}
			if kind == "duplicate" {
				receipt.Objects = 2
			}
			if kind == "wrong-count" {
				receipt.Objects = 2
			}
			if kind == "digest" {
				data[0] ^= 1
			}
			parent := snapshotDirectory(t)
			archive, destination := filepath.Join(parent, "image.tar"), filepath.Join(parent, "restored")
			require.NoError(t, os.WriteFile(archive, data, 0o600))
			result, err := RestoreFilesystem(t.Context(), destination, archive, "restore", receipt)
			require.Error(t, err)
			require.False(t, result.Published)
			_, err = os.Stat(destination)
			require.ErrorIs(t, err, os.ErrNotExist)
			entries, err := os.ReadDir(parent)
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

func TestBlobSnapshotLegacyObjectsRequireExplicitMigration(t *testing.T) {
	source := snapshotObjects(t)
	for key, payload := range map[string]string{"mutable": "mutable bytes", "retained-v1/live": liveEnvelope + strings.Repeat("payload", 900000), "retained-v1/empty": liveEnvelope, "retained-v1/retired-without-record": retiredEnvelope} {
		_, err := source.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(source.bucket), Key: aws.String(source.objectKey(key)), Body: strings.NewReader(payload)})
		require.NoError(t, err)
	}
	_, err := source.Get(t.Context(), "mutable")
	require.ErrorIs(t, err, ErrLayoutMigrationRequired)
	archive := filepath.Join(snapshotDirectory(t), "legacy.tar")
	receipt, err := BackupLegacyObjects(t.Context(), source, archive)
	require.NoError(t, err)
	require.Equal(t, int64(4), receipt.Objects)
	require.Equal(t, int64(1), receipt.Retired)
	require.True(t, isAbsent(source.readLayout(t.Context())))
	target := filepath.Join(snapshotDirectory(t), "restored")
	result, err := RestoreFilesystem(t.Context(), target, archive, "migration", receipt)
	require.NoError(t, err)
	require.True(t, result.Published)
	checkSnapshot(t, activateSnapshotFixture(t, &Filesystem{root: target}))
	// Export leaves both current objects and all prior versions untouched.
	versions, err := source.client.ListObjectVersions(t.Context(), &s3.ListObjectVersionsInput{Bucket: aws.String(source.bucket)})
	require.NoError(t, err)
	require.Len(t, versions.Versions, 4)
	require.Empty(t, versions.DeleteMarkers)
}

func TestBlobSnapshotInterruptedObjectImport(t *testing.T) {
	source, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
	require.NoError(t, err)
	seedSnapshot(t, source)
	archive := filepath.Join(snapshotDirectory(t), "image.tar")
	receipt, err := Backup(t.Context(), source, archive)
	require.NoError(t, err)
	target := snapshotObjects(t)
	claim, err := makeBlobClaim("interrupted", receipt)
	require.NoError(t, err)
	require.NoError(t, target.claimImport(t.Context(), claim))
	// The durable marker and one transferred object survive the original client.
	_, err = target.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(target.bucket), Key: aws.String(target.objectKey(blobAddress(retainedDir, "retired-without-record"))), Body: strings.NewReader(retiredEnvelope), Metadata: map[string]string{retainedObjectMetadataKey: "retired-v1"}})
	require.NoError(t, err)
	require.ErrorIs(t, target.probeLayout(t.Context()), ErrImportRestricted)
	require.NoError(t, RestoreObjectStore(t.Context(), target, archive, snapshotDirectory(t), "interrupted", receipt))
	checkSnapshot(t, activateSnapshotFixture(t, target))
}

func TestBlobSnapshotObjectImportRefusesConflictingBytes(t *testing.T) {
	source, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
	require.NoError(t, err)
	seedSnapshot(t, source)
	archive := filepath.Join(snapshotDirectory(t), "image.tar")
	receipt, err := Backup(t.Context(), source, archive)
	require.NoError(t, err)
	target := snapshotObjects(t)
	claim, err := makeBlobClaim("interrupted", receipt)
	require.NoError(t, err)
	require.NoError(t, target.claimImport(t.Context(), claim))
	address := target.objectKey(blobAddress(objectsDir, "mutable"))
	_, err = target.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(target.bucket), Key: aws.String(address), Body: strings.NewReader("foreign bytes")})
	require.NoError(t, err)
	require.Error(t, RestoreObjectStore(t.Context(), target, archive, snapshotDirectory(t), "interrupted", receipt))
	require.ErrorIs(t, target.probeLayout(t.Context()), ErrImportRestricted)
	stored, err := target.client.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(target.bucket), Key: aws.String(address)})
	require.NoError(t, err)
	data, err := io.ReadAll(stored.Body)
	require.NoError(t, err)
	require.NoError(t, stored.Body.Close())
	require.Equal(t, "foreign bytes", string(data))
}

func TestBlobSnapshotObjectImportValidatesBeforeClaim(t *testing.T) {
	target := snapshotObjects(t)
	parent := snapshotDirectory(t)
	archive := filepath.Join(parent, "image.tar")
	data := make([]byte, 1024)
	require.NoError(t, os.WriteFile(archive, data, 0o600))
	receipt := Snapshot{Format: blobSnapshotFormat, Size: 1024, SHA256: strings.Repeat("0", 64)}
	require.Error(t, RestoreObjectStore(t.Context(), target, archive, parent, "invalid", receipt))
	contents, err := target.client.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{Bucket: aws.String(target.bucket)})
	require.NoError(t, err)
	require.Empty(t, contents.Contents)
}

func TestBlobSnapshotObjectImportCompetingOwners(t *testing.T) {
	source, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
	require.NoError(t, err)
	archive := filepath.Join(snapshotDirectory(t), "empty.tar")
	receipt, err := Backup(t.Context(), source, archive)
	require.NoError(t, err)
	target := snapshotObjects(t)
	start, results := make(chan struct{}), make(chan error, 2)
	for _, operation := range []string{"first", "second"} {
		scratch := snapshotDirectory(t)
		go func() {
			<-start
			results <- RestoreObjectStore(t.Context(), target, archive, scratch, operation, receipt)
		}()
	}
	close(start)
	first, second := <-results, <-results
	require.True(t, (first == nil) != (second == nil), "exactly one owner must succeed: %v, %v", first, second)
	require.ErrorIs(t, target.probeLayout(t.Context()), ErrImportRestricted)
}

func TestBlobSnapshotRefusesUnexpectedFilesystemEntries(t *testing.T) {
	for _, kind := range []string{"symlink", "bad-address", "bad-envelope"} {
		t.Run(kind, func(t *testing.T) {
			source, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
			require.NoError(t, err)
			address := blobAddress(objectsDir, "object")
			if kind == "bad-address" {
				address = "objects/unexpected"
			}
			if kind == "bad-envelope" {
				address = blobAddress(retainedDir, "object")
			}
			path := filepath.Join(source.root, filepath.FromSlash(address))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			if kind == "symlink" {
				outside := filepath.Join(snapshotDirectory(t), "outside")
				require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
				require.NoError(t, os.Symlink(outside, path))
			} else {
				require.NoError(t, os.WriteFile(path, []byte("bad payload"), 0o600))
			}
			receipt, err := Backup(t.Context(), source, filepath.Join(snapshotDirectory(t), "image.tar"))
			require.Error(t, err)
			require.Empty(t, receipt.Format)
		})
	}
}

func TestBlobSnapshotObjectImportRefusesUnlistedObject(t *testing.T) {
	source, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
	require.NoError(t, err)
	archive := filepath.Join(snapshotDirectory(t), "empty.tar")
	receipt, err := Backup(t.Context(), source, archive)
	require.NoError(t, err)
	target := snapshotObjects(t)
	claim, err := makeBlobClaim("resume", receipt)
	require.NoError(t, err)
	require.NoError(t, target.claimImport(t.Context(), claim))
	_, err = target.client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(target.bucket), Key: aws.String(target.objectKey(blobAddress(objectsDir, "unlisted"))), Body: strings.NewReader("foreign")})
	require.NoError(t, err)
	require.Error(t, RestoreObjectStore(t.Context(), target, archive, snapshotDirectory(t), "resume", receipt))
	require.ErrorIs(t, target.probeLayout(t.Context()), ErrImportRestricted)
}
