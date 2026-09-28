package jobs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/jobs/fileio"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type aggregateFiles struct {
	fileio.Store
	input   string
	loseAck *atomic.Bool
}

func (f aggregateFiles) OpenInput(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.input)), nil
}
func (f aggregateFiles) StoreAggregate(ctx context.Context, b jobs.Batch, failed bool, size int64, digest string, r io.Reader) (string, error) {
	id, err := f.Store.StoreAggregate(ctx, b, failed, size, digest, r)
	if err == nil && f.loseAck != nil && f.loseAck.Load() {
		return "", errors.New("aggregate acknowledgment lost")
	}
	return id, err
}

func TestBatchAggregateRecoversLostPublicationWithoutReplay(t *testing.T) {
	repotest.Run(t, func(t *testing.T, kv storage.KVStore) {
		records, err := jobs.OpenBatchRepository(kv)
		require.NoError(t, err)
		fileRecords, err := files.OpenRepository(kv)
		require.NoError(t, err)
		blobs, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		meter, err := storedbytes.NewStorageMeter(kv)
		require.NoError(t, err)
		stored, err := files.NewService(fileRecords, blobs, files.WithMeter(meter))
		require.NoError(t, err)
		lost := &atomic.Bool{}
		lost.Store(true)
		ioFiles := aggregateFiles{Store: fileio.Store{Files: stored, Account: "a"}, input: "{}\n{}\n{}\n", loseAck: lost}
		service, err := jobs.NewBatchService(records, jobs.WithBatchConcurrency(3))
		require.NoError(t, err)
		runner := &echoRunner{}
		batch, err := service.Submit(t.Context(), jobs.BatchSubmission{ID: "aggregate", Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: ioFiles, Runner: runner})
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			b, e := records.Get(t.Context(), "a", batch.ID)
			return e == nil && b.State == jobs.JobStateFailed
		}, 5*time.Second, 5*time.Millisecond)
		// Restart recovery begins after the old worker releases its dependencies.
		require.NoError(t, service.Close(t.Context()))
		all, err := fileRecords.List(t.Context(), "a", 100)
		require.NoError(t, err)
		var original string
		for _, f := range all {
			if f.Filename == "aggregate_output.jsonl" {
				require.Empty(t, original)
				original = f.ID
			}
		}
		require.NotEmpty(t, original)
		for number := 1; number <= 3; number++ {
			line, e := records.ReadLine(t.Context(), "a", batch.ID, number)
			require.NoError(t, e)
			require.True(t, line.ResultReady)
			_, reader, e := stored.Open(t.Context(), "a", line.OutputFileID)
			require.NoError(t, e)
			require.NoError(t, reader.Close())
		}
		lost.Store(false)
		reopened, err := jobs.NewBatchService(records, jobs.WithBatchFiles(func(jobs.Batch) jobs.BatchIO { return ioFiles }))
		require.NoError(t, err)
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		final, err := reopened.Get(t.Context(), "a", batch.ID)
		require.NoError(t, err)
		require.True(t, final.RunFinished)
		require.True(t, final.ResultsReleased)
		require.Equal(t, original, final.OutputFileID)
		require.Equal(t, 3, final.CompletedLines)
		require.Len(t, runner.ranLines(), 3)
		_, reader, err := stored.Open(t.Context(), "a", final.OutputFileID)
		require.NoError(t, err)
		content, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, "{\"line\":1}\n{\"line\":2}\n{\"line\":3}\n", string(content))
		visible, err := stored.List(t.Context(), "a", 100)
		require.NoError(t, err)
		require.Len(t, visible, 1)
		total, err := meter.Total(t.Context(), "a")
		require.NoError(t, err)
		require.Equal(t, int64(len(content)), total)
		again, err := reopened.RecoverResults(t.Context(), "a", batch.ID, ioFiles)
		require.NoError(t, err)
		require.Equal(t, final.OutputFileID, again.OutputFileID)
	})
}

type countingInput struct {
	*memoryBatchIO
	entered, release chan struct{}
	blocked          atomic.Bool
}

func (b *countingInput) OpenInput(ctx context.Context) (io.ReadCloser, error) {
	if b.blocked.CompareAndSwap(false, true) {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return b.memoryBatchIO.OpenInput(ctx)
}
func TestAggregateRecoveryCannotFinishBeforeInputCount(t *testing.T) {
	records, err := jobs.OpenBatchRepository(storage.NewMockStore())
	require.NoError(t, err)
	input := &countingInput{memoryBatchIO: newMemoryBatchIO("{}\n"), entered: make(chan struct{}), release: make(chan struct{})}
	service, err := jobs.NewBatchService(records, jobs.WithBatchFiles(func(jobs.Batch) jobs.BatchIO { return input }))
	require.NoError(t, err)
	batch, err := service.Submit(t.Context(), jobs.BatchSubmission{ID: "counting", Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: input, Runner: &echoRunner{}})
	require.NoError(t, err)
	<-input.entered
	read, readErr := service.Get(t.Context(), "a", batch.ID)
	_, recoveryErr := service.RecoverResults(t.Context(), "a", batch.ID, input)
	close(input.release)
	require.NoError(t, readErr)
	require.ErrorIs(t, recoveryErr, jobs.ErrBatchResultsPending)
	require.False(t, read.RunFinished)
	require.Equal(t, jobs.JobStateRunning, read.State)
	final := waitForTerminalBatch(t, service, "a", batch.ID)
	require.Equal(t, 1, final.CompletedLines)
}

// retainedBatch seeds durable results without starting a worker. Recovery has
// no runner to call, so each case also proves that publication cannot dispatch.
func retainedBatch(t *testing.T, kv storage.KVStore) (jobs.BatchRepository, *files.Service, *storedbytes.StorageMeter, fileio.Store, jobs.Batch) {
	t.Helper()
	return retainedBatchAt(t, kv, t.TempDir())
}

func retainedBatchAt(t *testing.T, kv storage.KVStore, root string, options ...files.Option) (jobs.BatchRepository, *files.Service, *storedbytes.StorageMeter, fileio.Store, jobs.Batch) {
	t.Helper()
	b, err := blob.NewFilesystem(root)
	require.NoError(t, err)
	return retainedBatchOn(t, kv, b, options...)
}

func retainedBatchOn(t *testing.T, kv storage.KVStore, b blob.Store, options ...files.Option) (jobs.BatchRepository, *files.Service, *storedbytes.StorageMeter, fileio.Store, jobs.Batch) {
	t.Helper()
	r, err := jobs.OpenBatchRepository(kv)
	require.NoError(t, err)
	fr, err := files.OpenRepository(kv)
	require.NoError(t, err)
	meter, err := storedbytes.NewStorageMeter(kv)
	require.NoError(t, err)
	fs, err := files.NewService(fr, b, append(options, files.WithMeter(meter))...)
	require.NoError(t, err)
	adapter := fileio.Store{Files: fs, Account: "a", StoredBytesBound: 64}
	batch, err := jobs.NewBatch("retained", "a", "/v1/chat/completions", "input", time.Now().UTC())
	require.NoError(t, err)
	batch.TotalLines = 2
	batch.StoredBytesBound = 64
	require.NoError(t, batch.Transition(jobs.JobStateRunning, time.Now()))
	require.NoError(t, r.Create(t.Context(), batch))
	for number := 1; number <= 2; number++ {
		body := fmt.Sprintf(`{"line":%d}`, number)
		digest := sha256.Sum256([]byte(body))
		encoded := hex.EncodeToString(digest[:])
		line, err := r.ClaimLine(t.Context(), "a", batch.ID, number, encoded)
		require.NoError(t, err)
		file, err := adapter.PrepareResult(t.Context(), line)
		require.NoError(t, err)
		line, err = r.BindLineOutput(t.Context(), line, file)
		require.NoError(t, err)
		line, err = r.RecordLineResult(t.Context(), line, encoded, int64(len(body)), number == 2)
		require.NoError(t, err)
		require.NoError(t, adapter.StoreResult(t.Context(), file.ID, int64(len(body)), encoded, strings.NewReader(body)))
		_, err = r.ConfirmLineResult(t.Context(), line)
		require.NoError(t, err)
	}
	batch, err = r.Get(t.Context(), "a", batch.ID)
	require.NoError(t, err)
	return r, fs, meter, adapter, batch
}

func TestBatchAggregateCleanupRequiresCompleteReferences(t *testing.T) {
	r, fs, _, adapter, batch := retainedBatch(t, storage.NewMockStore())
	next := batch
	next.RunFinished = true
	require.NoError(t, next.Transition(jobs.JobStateCompleted, time.Now()))
	require.NoError(t, r.Replace(t.Context(), batch, next))
	service, err := jobs.NewBatchService(r)
	require.NoError(t, err)
	_, err = service.RecoverResults(t.Context(), "a", batch.ID, adapter)
	require.ErrorIs(t, err, jobs.ErrCorruptBatchRecord)
	for number := 1; number <= 2; number++ {
		line, err := r.ReadLine(t.Context(), "a", batch.ID, number)
		require.NoError(t, err)
		_, reader, err := fs.Open(t.Context(), "a", line.OutputFileID)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
	}
}

type aggregateFaultRepository struct {
	jobs.BatchRepository
	fail *atomic.Bool
}

func (r aggregateFaultRepository) Replace(ctx context.Context, before, after jobs.Batch) error {
	if after.RunFinished && r.fail.Load() {
		return errors.New("batch publication record unavailable")
	}
	return r.BatchRepository.Replace(ctx, before, after)
}

type aggregateFaultFiles struct {
	fileio.Store
	confirm, cleanup *atomic.Bool
}

func (f aggregateFaultFiles) ConfirmAggregate(ctx context.Context, id string) error {
	if f.confirm.Load() {
		return errors.New("aggregate confirmation unavailable")
	}
	return f.Store.ConfirmAggregate(ctx, id)
}
func (f aggregateFaultFiles) DeleteResult(ctx context.Context, id string) error {
	err := f.Store.DeleteResult(ctx, id)
	if err == nil && f.cleanup.CompareAndSwap(true, false) {
		return errors.New("checkpoint retirement acknowledgment lost")
	}
	return err
}

func TestBatchAggregateRecoveryRetainsResultsAcrossFailures(t *testing.T) {
	for _, stage := range []string{"metadata", "confirmation", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, kv storage.KVStore) {
				r, fs, meter, adapter, batch := retainedBatch(t, kv)
				metadata, confirm, cleanup := &atomic.Bool{}, &atomic.Bool{}, &atomic.Bool{}
				metadata.Store(stage == "metadata")
				confirm.Store(stage == "confirmation")
				cleanup.Store(stage == "cleanup")
				faults := aggregateFaultFiles{Store: adapter, confirm: confirm, cleanup: cleanup}
				s, err := jobs.NewBatchService(aggregateFaultRepository{BatchRepository: r, fail: metadata})
				require.NoError(t, err)
				_, err = s.RecoverResults(t.Context(), "a", batch.ID, faults)
				require.Error(t, err)
				partial, err := r.Get(t.Context(), "a", batch.ID)
				require.NoError(t, err)
				require.False(t, partial.ResultsReleased)
				for number := 1; number <= 2; number++ {
					line, err := r.ReadLine(t.Context(), "a", batch.ID, number)
					require.NoError(t, err)
					_, reader, err := fs.Open(t.Context(), "a", line.OutputFileID)
					if stage == "cleanup" && number == 1 {
						require.ErrorIs(t, err, files.ErrFileNotFound)
					} else {
						require.NoError(t, err)
						require.NoError(t, reader.Close())
					}
				}
				metadata.Store(false)
				confirm.Store(false)
				restarted, err := jobs.NewBatchService(r)
				require.NoError(t, err)
				final, err := restarted.RecoverResults(t.Context(), "a", batch.ID, adapter)
				require.NoError(t, err)
				require.True(t, final.ResultsReleased)
				require.Equal(t, 1, final.CompletedLines)
				require.Equal(t, 1, final.FailedLines)
				for number, id := range []string{final.OutputFileID, final.ErrorFileID} {
					_, reader, err := fs.Open(t.Context(), "a", id)
					require.NoError(t, err)
					body, err := io.ReadAll(reader)
					require.NoError(t, err)
					require.NoError(t, reader.Close())
					require.Equal(t, fmt.Sprintf("{\"line\":%d}\n", number+1), string(body))
				}
				visible, err := fs.List(t.Context(), "a", 100)
				require.NoError(t, err)
				require.Len(t, visible, 2)
				total, err := meter.Total(t.Context(), "a")
				require.NoError(t, err)
				require.Equal(t, int64(len("{\"line\":1}\n{\"line\":2}\n")), total)
			})
		})
	}
}

func TestBatchAggregateConcurrentRecovery(t *testing.T) {
	repotest.Run(t, func(t *testing.T, kv storage.KVStore) {
		r, fs, meter, adapter, batch := retainedBatch(t, kv)
		var workers sync.WaitGroup
		for range 8 {
			service, err := jobs.NewBatchService(r)
			require.NoError(t, err)
			workers.Go(func() {
				// An overlapping pass can lose access to a checkpoint after
				// another pass has confirmed and retired it. A retry must settle.
				_, _ = service.RecoverResults(t.Context(), "a", batch.ID, adapter)
			})
		}
		workers.Wait()
		service, err := jobs.NewBatchService(r)
		require.NoError(t, err)
		final, err := service.RecoverResults(t.Context(), "a", batch.ID, adapter)
		require.NoError(t, err)
		require.True(t, final.ResultsReleased)
		visible, err := fs.List(t.Context(), "a", 100)
		require.NoError(t, err)
		require.Len(t, visible, 2)
		total, err := meter.Total(t.Context(), "a")
		require.NoError(t, err)
		require.Equal(t, int64(22), total)
	})
}

func TestBatchAggregateSweepDoesNotReportActiveWorkAsFailure(t *testing.T) {
	r, err := jobs.OpenBatchRepository(storage.NewMockStore())
	require.NoError(t, err)
	batch, err := jobs.NewBatch("queued", "a", "/v1/chat/completions", "input", time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, r.Create(t.Context(), batch))
	s, err := jobs.NewBatchService(r, jobs.WithBatchFiles(func(jobs.Batch) jobs.BatchIO { return newMemoryBatchIO("") }))
	require.NoError(t, err)
	result, err := s.Sweep(t.Context())
	require.NoError(t, err)
	require.Zero(t, result.Failed)
	stillQueued, err := r.Get(t.Context(), "a", batch.ID)
	require.NoError(t, err)
	require.Equal(t, batch, stillQueued)
}

type aggregateProcessFiles struct {
	fileio.Store
	marker string
}

func (f aggregateProcessFiles) StoreAggregate(ctx context.Context, batch jobs.Batch, failed bool, size int64, digest string, r io.Reader) (string, error) {
	id, err := f.Store.StoreAggregate(ctx, batch, failed, size, digest, r)
	if err == nil {
		if err := os.WriteFile(f.marker, []byte(id), 0600); err != nil {
			return "", err
		}
		select {}
	}
	return id, err
}

type aggregateProcessRepository struct {
	jobs.BatchRepository
	marker string
}

func (r aggregateProcessRepository) Replace(ctx context.Context, before, after jobs.Batch) error {
	err := r.BatchRepository.Replace(ctx, before, after)
	if err == nil && after.RunFinished && !after.ResultsReleased {
		if err := os.WriteFile(r.marker, []byte(after.OutputFileID), 0600); err != nil {
			return err
		}
		select {}
	}
	return err
}

func TestBatchAggregateSurvivesProcessLoss(t *testing.T) {
	const childKey = "STARPORT_BATCH_AGGREGATE_PROBE_CHILD"
	const stageKey = "STARPORT_BATCH_AGGREGATE_PROBE_STAGE"
	openKV := func(t *testing.T, root string) storage.KVStore {
		t.Helper()
		kv, err := storage.OpenBadger(storage.BadgerConfig{Path: filepath.Join(root, "kv"), SyncWrites: true, Compression: "snappy", NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, kv.Close()) })
		return kv
	}
	if root := os.Getenv(childKey); root != "" {
		kv := openKV(t, root)
		r, _, _, adapter, batch := retainedBatchAt(t, kv, filepath.Join(root, "blobs"))
		marker := filepath.Join(root, "marker")
		var batchIO jobs.BatchIO = adapter
		if os.Getenv(stageKey) == "publication" {
			batchIO = aggregateProcessFiles{Store: adapter, marker: marker}
		} else {
			r = aggregateProcessRepository{BatchRepository: r, marker: marker}
		}
		s, err := jobs.NewBatchService(r)
		require.NoError(t, err)
		_, err = s.RecoverResults(t.Context(), "a", batch.ID, batchIO)
		t.Fatalf("child returned before interruption: %v", err)
	}
	for _, stage := range []string{"publication", "metadata"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			var output bytes.Buffer
			command := exec.Command(os.Args[0], "-test.run=^TestBatchAggregateSurvivesProcessLoss$", "-test.timeout=25s")
			command.Env = append(os.Environ(), childKey+"="+root, stageKey+"="+stage)
			command.Stdout, command.Stderr = &output, &output
			require.NoError(t, command.Start())
			t.Cleanup(func() { _ = command.Process.Kill() })
			var original []byte
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				original, _ = os.ReadFile(filepath.Join(root, "marker"))
				if len(original) != 0 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			require.NoError(t, command.Process.Kill())
			require.Error(t, command.Wait())
			require.NotEmpty(t, original, "child never reached boundary: %s", output.String())
			kv := openKV(t, root)
			r, err := jobs.OpenBatchRepository(kv)
			require.NoError(t, err)
			fr, err := files.OpenRepository(kv)
			require.NoError(t, err)
			blobs, err := blob.NewFilesystem(filepath.Join(root, "blobs"))
			require.NoError(t, err)
			meter, err := storedbytes.NewStorageMeter(kv)
			require.NoError(t, err)
			fs, err := files.NewService(fr, blobs, files.WithMeter(meter))
			require.NoError(t, err)
			adapter := fileio.Store{Files: fs, Account: "a"}
			s, err := jobs.NewBatchService(r)
			require.NoError(t, err)
			final, err := s.RecoverResults(t.Context(), "a", "retained", adapter)
			require.NoError(t, err)
			require.True(t, final.ResultsReleased)
			require.Equal(t, string(original), final.OutputFileID)
			for number, id := range []string{final.OutputFileID, final.ErrorFileID} {
				_, reader, err := fs.Open(t.Context(), "a", id)
				require.NoError(t, err)
				content, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
				require.Equal(t, fmt.Sprintf("{\"line\":%d}\n", number+1), string(content))
			}
			total, err := meter.Total(t.Context(), "a")
			require.NoError(t, err)
			require.Equal(t, int64(22), total)
		})
	}
}

func TestBatchAggregateRespectsPeakQuotaAndOriginalBound(t *testing.T) {
	r, fs, meter, adapter, batch := retainedBatch(t, storage.NewMockStore())
	other, err := fs.Upload(t.Context(), files.UploadRequest{Account: "a", Filename: "other.txt", Purpose: files.PurposeUserData, StoredBytesBound: 64}, strings.NewReader(strings.Repeat("x", 30)))
	require.NoError(t, err)
	s, err := jobs.NewBatchService(r)
	require.NoError(t, err)
	// Restart configuration cannot widen the bound retained by the batch.
	adapter.StoredBytesBound = 1000
	_, err = s.RecoverResults(t.Context(), "a", batch.ID, adapter)
	require.ErrorIs(t, err, storedbytes.ErrStorageFull)
	partial, err := r.Get(t.Context(), "a", batch.ID)
	require.NoError(t, err)
	require.False(t, partial.RunFinished)
	total, err := meter.Total(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, int64(61), total)
	require.NoError(t, fs.Delete(t.Context(), "a", other.ID))
	final, err := s.RecoverResults(t.Context(), "a", batch.ID, adapter)
	require.NoError(t, err)
	require.True(t, final.ResultsReleased)
	total, err = meter.Total(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, int64(22), total)
}

type corruptAggregateInput struct{ fileio.Store }

func (f corruptAggregateInput) OpenResult(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(`{"line":9}`)), nil
}
func TestBatchAggregateRejectsCorruptOrExpiredCheckpoints(t *testing.T) {
	for _, condition := range []string{"corrupt", "expired"} {
		t.Run(condition, func(t *testing.T) {
			now := time.Now().UTC()
			r, fs, _, adapter, batch := retainedBatchAt(t, storage.NewMockStore(), t.TempDir(), files.WithClock(func() time.Time { return now }))
			s, err := jobs.NewBatchService(r)
			require.NoError(t, err)
			var input jobs.BatchIO = corruptAggregateInput{Store: adapter}
			if condition == "expired" {
				line, err := r.ReadLine(t.Context(), "a", batch.ID, 1)
				require.NoError(t, err)
				now = line.OutputExpiresAt
				input = adapter
			}
			_, err = s.RecoverResults(t.Context(), "a", batch.ID, input)
			require.Error(t, err)
			if condition == "corrupt" {
				require.ErrorIs(t, err, jobs.ErrCorruptBatchRecord)
			}
			retained, err := r.Get(t.Context(), "a", batch.ID)
			require.NoError(t, err)
			require.False(t, retained.RunFinished)
			require.False(t, retained.ResultsReleased)
			visible, err := fs.List(t.Context(), "a", 100)
			require.NoError(t, err)
			require.Empty(t, visible)
		})
	}
}
