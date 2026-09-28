package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryNativeReceiptPreservesUnconfirmedSubmission(t *testing.T) {
	for _, mode := range []string{"missing", "retained", "wrong-owner", "wrong-digest", "extra-bytes"} {
		t.Run(mode, func(t *testing.T) {
			at := time.Now().UTC()
			job, err := New("one", "owner", "provider", "model", routing.OperationVideosGenerations, at.Add(-time.Minute))
			require.NoError(t, err)
			job.Native, job.SubmissionPending, job.CatalogGeneration = true, true, "generation"
			job.nativeReceiptKey, job.nativeAssetKey, job.nativeRetention, job.nativeAssetBound = "receipt", "asset", time.Hour, 100
			source, err := blob.NewFilesystem(filepath.Join(t.TempDir(), "blobs"))
			require.NoError(t, err)
			if mode != "missing" {
				body := []byte("video")
				digest := sha256.Sum256(body)
				receipt := nativeReceipt{Version: 1, JobID: job.ID, Account: job.Account, Provider: job.Provider, Model: job.Model, Generation: job.CatalogGeneration, RequestID: "private-provider-request", State: JobStateCompleted, RecordedAt: at, AssetBytes: int64(len(body)), AssetDigest: hex.EncodeToString(digest[:])}
				if mode == "wrong-owner" {
					receipt.Account = "another"
				}
				if mode == "wrong-digest" {
					receipt.AssetDigest = strings.Repeat("0", 64)
				}
				header, err := json.Marshal(receipt)
				require.NoError(t, err)
				var prefix [8]byte
				binary.BigEndian.PutUint64(prefix[:], uint64(len(header)))
				payload := append(append(prefix[:], header...), body...)
				if mode == "extra-bytes" {
					payload = append(payload, 1)
				}
				_, err = source.Publish(t.Context(), job.nativeReceiptKey, bytes.NewReader(payload))
				require.NoError(t, err)
			}
			data, err := encodeJob(job)
			require.NoError(t, err)
			got, err := VerifyRecoveryRecord(t.Context(), storageKey(job.Account, job.ID), data, source, at)
			if mode == "missing" || mode == "retained" {
				require.NoError(t, err)
				require.Equal(t, job, got)
				require.True(t, got.SubmissionPending)
			} else {
				require.ErrorIs(t, err, ErrCorruptRecord)
			}
		})
	}
}

func TestRecoveryBatchKeepsInterruptedResultAndRejectsLostClaim(t *testing.T) {
	store := storage.NewMockStore()
	blobs, err := blob.NewFilesystem(filepath.Join(t.TempDir(), "blobs"))
	require.NoError(t, err)
	fileRecords, err := files.OpenRepository(store)
	require.NoError(t, err)
	fileService, err := files.NewService(fileRecords, blobs)
	require.NoError(t, err)
	output, err := fileService.PrepareOutput(t.Context(), "owner", "line-one", "result.jsonl", 100)
	require.NoError(t, err)
	batch, err := NewBatch("batch", "owner", "/v1/chat/completions", "deleted-input", time.Now())
	require.NoError(t, err)
	batch.State, batch.TotalLines, batch.ClaimedLines = JobStateRunning, 1, 1
	parent, err := encodeBatch(batch)
	require.NoError(t, err)
	require.NoError(t, store.Set(t.Context(), batchStorageKey(batch.Account, batch.ID), parent))
	line := BatchLine{Version: 2, Account: batch.Account, BatchID: batch.ID, Number: 1, InputDigest: strings.Repeat("a", 64), RequestID: "attempt-one", OutputFileID: output.ID, OutputExpiresAt: output.ExpiresAt, ResultDigest: strings.Repeat("b", 64), ResultBytes: 7}
	data, err := json.Marshal(line)
	require.NoError(t, err)
	key := batchLineKey(line.Account, line.BatchID, line.Number)
	require.NoError(t, store.Set(t.Context(), key, data))
	retained, missing, err := VerifyRecoveryBatchLine(t.Context(), key, data, store)
	require.NoError(t, err)
	require.False(t, missing)
	require.Equal(t, line, retained)
	_, unavailable, err := VerifyRecoveryBatch(t.Context(), batchStorageKey(batch.Account, batch.ID), parent, store)
	require.NoError(t, err)
	require.EqualValues(t, 1, unavailable, "deleted input remains explicit reconciliation evidence")
	line.ResultReady = true
	data, err = json.Marshal(line)
	require.NoError(t, err)
	_, _, err = VerifyRecoveryBatchLine(t.Context(), key, data, store)
	require.ErrorIs(t, err, ErrCorruptBatchRecord, "pending output cannot satisfy confirmed result")
	require.NoError(t, store.Delete(t.Context(), key))
	_, _, err = VerifyRecoveryBatch(t.Context(), batchStorageKey(batch.Account, batch.ID), parent, store)
	require.ErrorIs(t, err, ErrCorruptBatchRecord, "a lost execution claim cannot become an unstarted line")
}
