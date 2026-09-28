package jobs_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type drainingBatchRunner struct {
	entered, release chan struct{}
	calls            atomic.Int32
	once             sync.Once
}

func (r *drainingBatchRunner) RunLine(ctx context.Context, _ jobs.BatchLine, _ []byte) ([]byte, bool) {
	r.calls.Add(1)
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return []byte("{}"), false
	case <-ctx.Done():
		return []byte("cancelled"), true
	}
}
func TestBatchShutdownDrainsAdmittedLineAndRetainsUntouchedWork(t *testing.T) {
	kv := storage.NewMockStore()
	records, err := jobs.OpenBatchRepository(kv)
	require.NoError(t, err)
	meter, err := jobslots.Open(kv)
	require.NoError(t, err)
	service, err := jobs.NewBatchService(records, jobs.WithBatchConcurrency(1), jobs.WithBatchJobMeter(meter))
	require.NoError(t, err)
	runner := &drainingBatchRunner{entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(runner.release) })
	defer release()
	batch, err := service.Submit(t.Context(), jobs.BatchSubmission{Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", OutstandingBound: 1, IO: newMemoryBatchIO("{}\n{}\n"), Runner: runner})
	require.NoError(t, err)
	select {
	case <-runner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("line did not dispatch")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, service.Close(ctx), context.DeadlineExceeded)
	require.EqualValues(t, 1, runner.calls.Load())
	_, err = service.Submit(t.Context(), jobs.BatchSubmission{Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: newMemoryBatchIO("{}\n"), Runner: &echoRunner{}})
	require.ErrorIs(t, err, jobs.ErrServiceClosed)
	release()
	require.NoError(t, service.Close(t.Context()))
	require.NoError(t, service.Close(t.Context()))
	retained, err := records.Get(t.Context(), "a", batch.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateRunning, retained.State)
	require.False(t, retained.RunFinished)
	require.Equal(t, 1, retained.ClaimedLines)
	line, err := records.ReadLine(t.Context(), "a", batch.ID, 1)
	require.NoError(t, err)
	require.True(t, line.ResultReady)
	require.EqualValues(t, 1, runner.calls.Load())
	total, err := meter.Total(t.Context(), "a")
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
}
func TestBatchShutdownDoesNotRetainRejectedSubmission(t *testing.T) {
	records, err := jobs.OpenBatchRepository(storage.NewMockStore())
	require.NoError(t, err)
	service, err := jobs.NewBatchService(records)
	require.NoError(t, err)
	_, err = service.Submit(t.Context(), jobs.BatchSubmission{Endpoint: "/v1/chat/completions", InputFileID: "input", IO: newMemoryBatchIO("{}\n"), Runner: &echoRunner{}})
	require.ErrorIs(t, err, jobs.ErrInvalidBatch)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, service.Close(ctx))
}

type heldBatchCreation struct {
	jobs.BatchRepository
	entered, release chan struct{}
}

func (r heldBatchCreation) Create(ctx context.Context, b jobs.Batch) error {
	close(r.entered)
	select {
	case <-r.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return r.BatchRepository.Create(ctx, b)
}
func TestBatchShutdownWaitsForAdmittedSubmissionWrite(t *testing.T) {
	records, err := jobs.OpenBatchRepository(storage.NewMockStore())
	require.NoError(t, err)
	held := heldBatchCreation{BatchRepository: records, entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(held.release) })
	defer release()
	service, err := jobs.NewBatchService(held)
	require.NoError(t, err)
	runner := &echoRunner{}
	done := make(chan error, 1)
	go func() {
		_, err := service.Submit(t.Context(), jobs.BatchSubmission{Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: newMemoryBatchIO("{}\n"), Runner: runner})
		done <- err
	}()
	select {
	case <-held.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("submission did not enter metadata write")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, service.Close(ctx), context.DeadlineExceeded)
	release()
	require.NoError(t, <-done)
	require.NoError(t, service.Close(t.Context()))
	require.Empty(t, runner.ranLines(), "shutdown before dispatch cannot start paid work")
	all, err := records.List(t.Context(), "a", 10)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Zero(t, all[0].ClaimedLines)
	require.False(t, all[0].RunFinished)
}
