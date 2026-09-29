package jobs

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type executionReplaySnapshot map[string]storage.TransferRecord

func (s executionReplaySnapshot) ReadCaptured(ctx context.Context, key string, bound int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	r, ok := s[key]
	if !ok {
		return r, storage.ErrNotFound
	}
	if len(r.Value) > bound {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return r, nil
}
func replayBatchFixture(t *testing.T) (Batch, executionReplaySnapshot) {
	t.Helper()
	b, err := NewBatch("batch", "account", "/v1/chat/completions", "retained-input", time.Now().UTC())
	require.NoError(t, err)
	data, err := encodeBatch(b)
	require.NoError(t, err)
	key := batchStorageKey(b.Account, b.ID)
	return b, executionReplaySnapshot{key: {Key: key, Value: data}}
}
func replayLineFixture(b Batch, number int) BatchLine {
	return BatchLine{Version: 2, Account: b.Account, BatchID: b.ID, Number: number, InputDigest: strings.Repeat("a", 64), RequestID: fmt.Sprintf("original-request-%d", number)}
}
func openExecutionReplayStore(t *testing.T, backend string) storage.KVStore {
	t.Helper()
	var store storage.KVStore
	var err error
	if backend == "badger" {
		store, err = storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
	} else {
		address := os.Getenv("TEST_VALKEY_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
		}
		store, err = storage.OpenValkey(storage.ValkeyConfig{URL: address, DeploymentID: "batch-replay-" + rand.Text(), AllowInsecure: true})
	}
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func TestBatchReplayStagesLargeClaimHistory(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			batch, snapshot := replayBatchFixture(t)
			later := batch
			later.State = JobStateRunning
			later.TotalLines = 260
			later.ClaimedLines = 257
			target := openExecutionReplayStore(t, backend)
			identity := ""
			if p, ok := target.(storage.IncarnationProvider); ok {
				var err error
				identity, err = p.ObserveIncarnation(t.Context())
				require.NoError(t, err)
			}
			transfer, err := storage.OpenRecordTransfer(t.Context(), target, identity)
			require.NoError(t, err)
			claim := []byte("closed-batch-recovery")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			for _, record := range snapshot {
				require.NoError(t, transfer.Import(t.Context(), claim, record))
			}
			receipt := ""
			sequence := int64(0)
			apply := func(step BatchReplay) {
				t.Helper()
				changes, err := PrepareBatchReplay(t.Context(), snapshot, step)
				require.NoError(t, err)
				retry, err := PrepareBatchReplay(t.Context(), snapshot, step)
				require.NoError(t, err)
				require.Equal(t, changes, retry)
				sequence++
				next, err := transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), claim, sequence, receipt, strings.Repeat("a", 64), changes)
				require.NoError(t, err)
				repeated, err := transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), claim, sequence, receipt, strings.Repeat("a", 64), retry)
				require.NoError(t, err)
				require.Equal(t, next, repeated)
				receipt = next
				updated := maps.Clone(snapshot)
				for _, change := range changes {
					actual, err := target.Get(t.Context(), change.Key)
					require.NoError(t, err)
					require.Equal(t, change.NewValue, actual)
					updated[change.Key] = storage.TransferRecord{Key: change.Key, Value: actual}
				}
				snapshot = updated
				require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
			}
			for start := 1; start <= later.ClaimedLines; start += 127 {
				end := min(start+127, later.ClaimedLines+1)
				lines := make([]BatchLine, 0, end-start)
				for number := start; number < end; number++ {
					lines = append(lines, replayLineFixture(later, number))
				}
				apply(BatchReplay{Batch: later, Lines: lines})
				parent, err := target.Get(t.Context(), batchStorageKey(batch.Account, batch.ID))
				require.NoError(t, err)
				still, err := decodeBatch(parent)
				require.NoError(t, err)
				require.Zero(t, still.ClaimedLines)
			}
			apply(BatchReplay{Batch: later, Publish: true})
			records, err := OpenBatchRepository(target)
			require.NoError(t, err)
			restored, err := records.Get(t.Context(), batch.Account, batch.ID)
			require.NoError(t, err)
			require.Equal(t, 257, restored.ClaimedLines)
			for _, number := range []int{1, 127, 128, 257} {
				retained, err := records.ReadLine(t.Context(), batch.Account, batch.ID, number)
				require.NoError(t, err)
				require.Equal(t, replayLineFixture(later, number), retained)
				_, err = records.ClaimLine(t.Context(), batch.Account, batch.ID, number, retained.InputDigest)
				require.ErrorIs(t, err, ErrBatchLineClaimed)
			}
		})
	}
}

func TestBatchReplayRequiresCompleteClaimsBeforePublication(t *testing.T) {
	batch, before := replayBatchFixture(t)
	batch.State = JobStateRunning
	batch.TotalLines = 2
	batch.ClaimedLines = 2
	_, err := PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: batch, Lines: []BatchLine{replayLineFixture(batch, 1)}, Publish: true})
	require.Error(t, err)
	changes, err := PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: batch, Lines: []BatchLine{replayLineFixture(batch, 1)}})
	require.NoError(t, err)
	require.Len(t, changes, 1)
	_, err = PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: batch, Lines: []BatchLine{replayLineFixture(batch, 1), replayLineFixture(batch, 1)}})
	require.Error(t, err)
	_, err = PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: batch, Lines: make([]BatchLine, 128)})
	require.Error(t, err)
}

func TestBatchReplayRejectsHistoryRegression(t *testing.T) {
	batch, before := replayBatchFixture(t)
	batch.State = JobStateRunning
	batch.TotalLines = 3
	batch.ClaimedLines = 2
	data, err := encodeBatch(batch)
	require.NoError(t, err)
	key := batchStorageKey(batch.Account, batch.ID)
	before[key] = storage.TransferRecord{Key: key, Value: data}
	for _, mode := range []string{"claims", "input", "authorization", "key", "slot", "bound", "queued", "total"} {
		t.Run(mode, func(t *testing.T) {
			after := batch
			switch mode {
			case "claims":
				after.ClaimedLines = 1
			case "input":
				after.InputFileID = "other"
			case "authorization":
				after.Authorization = []byte("other")
			case "key":
				after.KeyID = "other"
			case "slot":
				after.SlotID = "other"
			case "bound":
				after.StoredBytesBound = 42
			case "queued":
				after.State = JobStateQueued
			case "total":
				after.TotalLines = 4
			}
			_, err := PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: after, Lines: []BatchLine{replayLineFixture(batch, 1)}})
			require.Error(t, err)
		})
	}
	line := replayLineFixture(batch, 1)
	line.OutputFileID = "result"
	line.OutputExpiresAt = time.Now().Add(time.Hour).UTC()
	line.ResultDigest = strings.Repeat("b", 64)
	line.ResultBytes = 7
	line.ResultReady = true
	raw, err := json.Marshal(line)
	require.NoError(t, err)
	lineKey := batchLineKey(line.Account, line.BatchID, line.Number)
	before[lineKey] = storage.TransferRecord{Key: lineKey, Value: raw}
	for _, mode := range []string{"request", "input", "output", "expiry", "digest", "bytes", "failure", "ready"} {
		t.Run("line-"+mode, func(t *testing.T) {
			after := line
			switch mode {
			case "request":
				after.RequestID = "other"
			case "input":
				after.InputDigest = strings.Repeat("c", 64)
			case "output":
				after.OutputFileID = "other"
			case "expiry":
				after.OutputExpiresAt = after.OutputExpiresAt.Add(time.Hour)
			case "digest":
				after.ResultDigest = strings.Repeat("c", 64)
			case "bytes":
				after.ResultBytes++
			case "failure":
				after.ResultFailed = true
			case "ready":
				after.ResultReady = false
			}
			_, err := PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: batch, Lines: []BatchLine{after}})
			require.Error(t, err)
		})
	}
}

func TestBatchReplayPreservesCompletedResults(t *testing.T) {
	batch, before := replayBatchFixture(t)
	batch.State = JobStateCompleted
	batch.TerminalAt = batch.CreatedAt.Add(time.Minute)
	batch.TotalLines, batch.ClaimedLines, batch.CompletedLines = 1, 1, 1
	batch.OutputFileID = "retained-aggregate"
	batch.RunFinished = true
	line := replayLineFixture(batch, 1)
	line.OutputFileID = "retained-result"
	line.OutputExpiresAt = batch.TerminalAt.Add(time.Hour)
	line.ResultDigest = strings.Repeat("b", 64)
	line.ResultBytes = 7
	line.ResultReady = true
	changes, err := PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: batch, Lines: []BatchLine{line}, Publish: true})
	require.NoError(t, err)
	require.Len(t, changes, 2)
	for _, change := range changes {
		before[change.Key] = storage.TransferRecord{Key: change.Key, Value: change.NewValue}
	}
	// If an operator deletes result files, retained execution claims remain.
	_, err = PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: batch, Publish: true})
	require.NoError(t, err)
	for _, mode := range []string{"terminal-time", "unfinish", "result-count", "output", "line-not-ready"} {
		t.Run(mode, func(t *testing.T) {
			after := batch
			changedLine := line
			switch mode {
			case "terminal-time":
				after.TerminalAt = after.TerminalAt.Add(time.Hour)
			case "unfinish":
				after.RunFinished = false
			case "result-count":
				after.CompletedLines = 0
			case "output":
				after.OutputFileID = "different"
			case "line-not-ready":
				changedLine.ResultReady = false
			}
			_, err := PrepareBatchReplay(t.Context(), before, BatchReplay{Batch: after, Lines: []BatchLine{changedLine}, Publish: true})
			require.Error(t, err)
		})
	}
}

func TestBatchReplayNewParentAndCapturedMetadata(t *testing.T) {
	batch, _ := replayBatchFixture(t)
	batch.State = JobStateRunning
	batch.TotalLines, batch.ClaimedLines = 1, 1
	empty := executionReplaySnapshot{}
	changes, err := PrepareBatchReplay(t.Context(), empty, BatchReplay{Batch: batch, Lines: []BatchLine{replayLineFixture(batch, 1)}, Publish: true})
	require.NoError(t, err)
	require.Len(t, changes, 2)
	for _, change := range changes {
		require.Nil(t, change.ExpectedValue)
		empty[change.Key] = storage.TransferRecord{Key: change.Key, Value: change.NewValue}
	}
	for _, mode := range []string{"expired-parent", "wrong-parent-key", "expired-line", "wrong-line-key", "unknown-parent-field", "unknown-line-field", "cancelled-context"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := maps.Clone(empty)
			key := batchStorageKey(batch.Account, batch.ID)
			if strings.Contains(mode, "line") {
				key = batchLineKey(batch.Account, batch.ID, 1)
			}
			record := snapshot[key]
			if strings.HasPrefix(mode, "unknown") {
				record.Value = append([]byte(`{"future_execution_fact":true,`), record.Value[1:]...)
			} else if strings.HasPrefix(mode, "expired") {
				record.ExpiresAtMillis = 1
			} else {
				record.Key += "-different"
			}
			snapshot[key] = record
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled-context" {
				cancel()
			}
			step := BatchReplay{Batch: batch, Publish: true}
			if mode == "unknown-line-field" {
				step.Lines = []BatchLine{replayLineFixture(batch, 1)}
			}
			changes, err := PrepareBatchReplay(ctx, snapshot, step)
			require.Error(t, err)
			require.Nil(t, changes)
		})
	}
}

type unboundedExecutionReplayReader struct{ executionReplaySnapshot }

func (s unboundedExecutionReplayReader) ReadCaptured(ctx context.Context, key string, _ int) (storage.TransferRecord, error) {
	return s.executionReplaySnapshot.ReadCaptured(ctx, key, storage.TransferMaxValueBytes)
}

func TestBatchReplayRejectsOversizedCapturedLine(t *testing.T) {
	batch, before := replayBatchFixture(t)
	batch.State = JobStateRunning
	batch.TotalLines, batch.ClaimedLines = 1, 1
	line := replayLineFixture(batch, 1)
	data, err := json.Marshal(line)
	require.NoError(t, err)
	key := batchLineKey(line.Account, line.BatchID, line.Number)
	before[key] = storage.TransferRecord{Key: key, Value: append(data, []byte(strings.Repeat(" ", 8193))...)}
	_, err = PrepareBatchReplay(t.Context(), unboundedExecutionReplayReader{before}, BatchReplay{Batch: batch, Lines: []BatchLine{line}})
	require.Error(t, err)
}
