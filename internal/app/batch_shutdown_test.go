package app

import (
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/jobs/fileio"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type heldBatchCleanup struct {
	fileio.Store
	entered, release chan struct{}
	once             sync.Once
}

func (f *heldBatchCleanup) OpenInput(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("{}\n")), nil
}
func (f *heldBatchCleanup) DeleteResult(ctx context.Context, id string) error {
	f.once.Do(func() { close(f.entered) })
	select {
	case <-f.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return f.Store.DeleteResult(ctx, id)
}

type shutdownBatchRunner struct{}

func (shutdownBatchRunner) RunLine(context.Context, jobs.BatchLine, []byte) ([]byte, bool) {
	return []byte("{}"), false
}

func TestBatchShutdownPreservesStorageUntilCleanupDrains(t *testing.T) {
	kv := storage.NewMockStore()
	records, err := jobs.OpenBatchRepository(kv)
	require.NoError(t, err)
	fr, err := files.OpenRepository(kv)
	require.NoError(t, err)
	bs, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	fs, err := files.NewService(fr, bs)
	require.NoError(t, err)
	service, err := jobs.NewBatchService(records)
	require.NoError(t, err)
	ioFiles := &heldBatchCleanup{Store: fileio.Store{Files: fs, Account: "a"}, entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(ioFiles.release) })
	defer release()
	batch, err := service.Submit(t.Context(), jobs.BatchSubmission{Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: ioFiles, Runner: shutdownBatchRunner{}})
	require.NoError(t, err)
	select {
	case <-ioFiles.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach checkpoint cleanup")
	}
	var closed atomic.Bool
	application := &App{batches: service}
	application.own("storage", func(context.Context) error { closed.Store(true); return nil })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err = application.Close(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, closed.Load(), "a timed-out drain must leave worker dependencies open")
	release()
	require.NoError(t, application.Close(t.Context()))
	require.True(t, closed.Load())
	final, err := records.Get(t.Context(), "a", batch.ID)
	require.NoError(t, err)
	require.True(t, final.ResultsReleased)
	_, err = service.Submit(t.Context(), jobs.BatchSubmission{Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: ioFiles, Runner: shutdownBatchRunner{}})
	require.ErrorIs(t, err, jobs.ErrServiceClosed)
}
