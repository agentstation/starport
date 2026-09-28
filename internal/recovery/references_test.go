package recovery

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBundleReferencesRequireMoreThanArtifactDigestsAndKeyChallenge(t *testing.T) {
	for _, mode := range []string{"valid", "different-historical-key", "missing-file-bytes", "missing-job-bytes"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, records, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = records
			key := source.Encryption
			if mode == "different-historical-key" {
				var err error
				key, err = credentials.NewEncryptionService([]byte(strings.Repeat("z", 32)))
				require.NoError(t, err)
			}
			secret, err := key.EncryptCredential(`{"api_key":"private-test-credential"}`)
			require.NoError(t, err)
			keys, err := credentials.Open(kv)
			require.NoError(t, err)
			_, err = keys.Create(t.Context(), credentials.ProviderKey{Scope: "owner", Provider: "former-provider", EncryptedCredential: secret})
			require.NoError(t, err)
			fileRecords, err := files.OpenRepository(kv)
			require.NoError(t, err)
			fileService, err := files.NewService(fileRecords, source.Blobs)
			require.NoError(t, err)
			inputFile, err := fileService.Upload(t.Context(), files.UploadRequest{Account: "owner", Filename: "input.txt", Purpose: files.PurposeBatch, Size: 5}, strings.NewReader("bytes"))
			require.NoError(t, err)
			batch, err := jobs.NewBatch("captured-batch", "owner", "/v1/chat/completions", inputFile.ID, time.Now())
			require.NoError(t, err)
			batch.State, batch.TotalLines = jobs.JobStateRunning, 1
			batches, err := jobs.OpenBatchRepository(kv)
			require.NoError(t, err)
			require.NoError(t, batches.Create(t.Context(), batch))
			_, err = batches.ClaimLine(t.Context(), batch.Account, batch.ID, 1, strings.Repeat("a", 64))
			require.NoError(t, err)
			job, err := jobs.New("one", "owner", "provider", "model", routing.OperationVideosGenerations, time.Now().Add(-time.Minute))
			require.NoError(t, err)
			require.NoError(t, job.Transition(jobs.JobStateCompleted, time.Now()))
			require.NoError(t, job.StoreAsset("job-asset", "video/mp4", 5, time.Now().Add(time.Hour)))
			jobRecords, err := jobs.OpenRepository(kv)
			require.NoError(t, err)
			require.NoError(t, jobRecords.Create(t.Context(), job))
			if mode != "missing-job-bytes" {
				_, err = source.Blobs.Publish(t.Context(), "job-asset", strings.NewReader("video"))
				require.NoError(t, err)
			}
			if mode == "missing-file-bytes" {
				source.Blobs, err = blob.NewFilesystem(filepath.Join(privateKVDirectory(t), "unmatched"))
				require.NoError(t, err)
			}
			manifest, err := BackupBundle(t.Context(), destination, source, request)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			// Byte integrity and access to the selected key alone cannot prove references.
			_, err = VerifyBundle(t.Context(), destination, digest, source.Encryption)
			require.NoError(t, err)
			_, report, err := InspectBundleReferences(t.Context(), destination, digest, privateKVDirectory(t), source.Encryption)
			if mode == "valid" {
				require.NoError(t, err)
				require.Equal(t, ReferenceReport{CredentialRecords: 1, CredentialValues: 1, FileRecords: 1, JobRecords: 1, BatchRecords: 1, BatchLines: 1, UnfinishedBatchLines: 1}, report)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-test-credential")
			}
		})
	}
}

func TestKVSnapshotViewBoundsReadsAndRemovesOnlyItsCopy(t *testing.T) {
	path, receipt := kvSnapshotFixture(t)
	scratch := privateKVDirectory(t)
	view, err := OpenKVSnapshot(t.Context(), path, scratch, receipt)
	require.NoError(t, err)
	value, err := view.GetBounded(t.Context(), "account:one", 1024)
	require.NoError(t, err)
	require.Equal(t, "encrypted-credentials", string(value))
	_, err = view.GetBounded(t.Context(), "account:one", 2)
	require.ErrorIs(t, err, storage.ErrValueTooLarge)
	_, err = view.GetBounded(t.Context(), "missing", 2)
	require.ErrorIs(t, err, storage.ErrNotFound)
	require.NoError(t, view.Close())
	require.FileExists(t, path)
}
