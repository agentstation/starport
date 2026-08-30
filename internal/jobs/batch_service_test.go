package jobs_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

// memoryBatchIO is a batch file seam over plain strings. It stands in for the
// file store, which the real composition supplies from outside this package.
type memoryBatchIO struct {
	input string

	mu     sync.Mutex
	stored map[string]string
	nextID int
}

func newMemoryBatchIO(input string) *memoryBatchIO {
	return &memoryBatchIO{input: input, stored: map[string]string{}}
}

func (m *memoryBatchIO) OpenInput(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(m.input)), nil
}

func (m *memoryBatchIO) StoreOutput(_ context.Context, name string, content io.Reader) (string, error) {
	data, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("file-%d", m.nextID)
	m.stored[id] = string(data)
	_ = name
	return id, nil
}

func (m *memoryBatchIO) file(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stored[id]
}

// echoRunner answers every line with a recognizable result. A line whose text
// contains "fail" answers as a failure, so a test chooses each line's file.
type echoRunner struct {
	mu    sync.Mutex
	lines []int
}

func (r *echoRunner) RunLine(_ context.Context, number int, line []byte) ([]byte, bool) {
	r.mu.Lock()
	r.lines = append(r.lines, number)
	r.mu.Unlock()
	failed := strings.Contains(string(line), "fail")
	return fmt.Appendf(nil, `{"line":%d}`, number), failed
}

func (r *echoRunner) ranLines() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.lines...)
}

func newBatchService(t *testing.T, options ...jobs.BatchServiceOption) *jobs.BatchService {
	t.Helper()
	repository, err := jobs.OpenBatchRepository(storage.NewMockStore())
	require.NoError(t, err)
	service, err := jobs.NewBatchService(repository, options...)
	require.NoError(t, err)
	return service
}

func waitForTerminalBatch(t *testing.T, service *jobs.BatchService, account, id string) jobs.Batch {
	t.Helper()
	var batch jobs.Batch
	require.Eventually(t, func() bool {
		record, err := service.Get(context.Background(), account, id)
		if err != nil {
			return false
		}
		batch = record
		return batch.State.Terminal() &&
			(batch.State != jobs.JobStateCompleted || batch.OutputFileID != "")
	}, 5*time.Second, 5*time.Millisecond)
	return batch
}

// TestAThreeLineBatchCompletesWithThreeResults is the acceptance shape: three
// request lines in, one output file with three result lines out.
func TestAThreeLineBatchCompletesWithThreeResults(t *testing.T) {
	service := newBatchService(t)
	batchIO := newMemoryBatchIO("{\"a\":1}\n\n{\"a\":2}\n{\"a\":3}\n")
	runner := &echoRunner{}

	batch, err := service.Submit(context.Background(), jobs.BatchSubmission{
		Account:     "account_a",
		Endpoint:    "/v1/chat/completions",
		InputFileID: "file-input",
		IO:          batchIO,
		Runner:      runner,
	})
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateQueued, batch.State)
	require.True(t, strings.HasPrefix(batch.ID, "batch-"))

	final := waitForTerminalBatch(t, service, "account_a", batch.ID)
	require.Equal(t, jobs.JobStateCompleted, final.State)
	require.Equal(t, 3, final.TotalLines)
	require.Equal(t, 3, final.CompletedLines)
	require.Zero(t, final.FailedLines)
	require.NotEmpty(t, final.OutputFileID)
	require.Empty(t, final.ErrorFileID, "a clean run stores no error file")
	require.False(t, final.TerminalAt.IsZero())

	output := batchIO.file(final.OutputFileID)
	require.Len(t, strings.Fields(output), 3)
	require.Contains(t, output, `{"line":1}`)
	require.Contains(t, output, `{"line":2}`)
	require.Contains(t, output, `{"line":3}`)
	require.ElementsMatch(t, []int{1, 2, 3}, runner.ranLines())
}

// TestAFailedLineLandsInTheErrorFileAndTheBatchStillCompletes holds the
// decision that a per-line failure is a result, not a batch failure.
func TestAFailedLineLandsInTheErrorFileAndTheBatchStillCompletes(t *testing.T) {
	service := newBatchService(t)
	batchIO := newMemoryBatchIO("{\"a\":1}\n{\"fail\":true}\n{\"a\":3}\n")

	batch, err := service.Submit(context.Background(), jobs.BatchSubmission{
		Account:     "account_a",
		Endpoint:    "/v1/embeddings",
		InputFileID: "file-input",
		IO:          batchIO,
		Runner:      &echoRunner{},
	})
	require.NoError(t, err)

	final := waitForTerminalBatch(t, service, "account_a", batch.ID)
	require.Equal(t, jobs.JobStateCompleted, final.State)
	require.Equal(t, 3, final.TotalLines)
	require.Equal(t, 2, final.CompletedLines)
	require.Equal(t, 1, final.FailedLines)
	require.NotEmpty(t, final.ErrorFileID)
	require.Len(t, strings.Fields(batchIO.file(final.OutputFileID)), 2)
	require.Equal(t, "{\"line\":2}\n", batchIO.file(final.ErrorFileID))
}

// blockingRunner holds every line until the test releases it, so the test
// controls exactly which lines are in flight when a cancel lands.
type blockingRunner struct {
	started chan int
	release chan struct{}

	mu    sync.Mutex
	lines []int
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{started: make(chan int, 16), release: make(chan struct{})}
}

func (r *blockingRunner) RunLine(_ context.Context, number int, _ []byte) ([]byte, bool) {
	r.mu.Lock()
	r.lines = append(r.lines, number)
	r.mu.Unlock()
	r.started <- number
	<-r.release
	return fmt.Appendf(nil, `{"line":%d}`, number), false
}

func (r *blockingRunner) ranLines() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.lines...)
}

// TestACancelMidRunStartsNoNewLines is the acceptance cancel: the batch
// reaches cancelled, the line in flight drains into the output file, and the
// lines behind it never start.
func TestACancelMidRunStartsNoNewLines(t *testing.T) {
	service := newBatchService(t, jobs.WithBatchConcurrency(1))
	batchIO := newMemoryBatchIO("{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n")
	runner := newBlockingRunner()

	batch, err := service.Submit(context.Background(), jobs.BatchSubmission{
		Account:     "account_a",
		Endpoint:    "/v1/chat/completions",
		InputFileID: "file-input",
		IO:          batchIO,
		Runner:      runner,
	})
	require.NoError(t, err)

	// The first line is in flight and the second has not started, because the
	// concurrency bound is one and the runner is holding the first line open.
	require.Equal(t, 1, <-runner.started)

	cancelled, err := service.Cancel(context.Background(), "account_a", batch.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCancelled, cancelled.State)

	close(runner.release)
	var final jobs.Batch
	require.Eventually(t, func() bool {
		final, err = service.Get(context.Background(), "account_a", batch.ID)
		return err == nil && final.OutputFileID != ""
	}, 5*time.Second, 5*time.Millisecond)

	require.Equal(t, jobs.JobStateCancelled, final.State)
	require.Equal(t, []int{1}, runner.ranLines(), "no new line may start after a cancel")
	require.Equal(t, 1, final.CompletedLines)
	require.Equal(t, "{\"line\":1}\n", batchIO.file(final.OutputFileID))

	// A second cancel answers that the batch already ended.
	_, err = service.Cancel(context.Background(), "account_a", batch.ID)
	require.ErrorIs(t, err, jobs.ErrBatchAlreadyEnded)
}

// TestALinePastTheByteBoundFailsTheWholeBatch holds the bound decision: a
// scanner that cannot read one line cannot say what the rest of the file
// holds, so the batch fails with a reason naming the bound.
func TestALinePastTheByteBoundFailsTheWholeBatch(t *testing.T) {
	service := newBatchService(t, jobs.WithBatchLineBytes(128))
	batchIO := newMemoryBatchIO("{\"a\":\"" + strings.Repeat("x", 256) + "\"}\n")

	batch, err := service.Submit(context.Background(), jobs.BatchSubmission{
		Account:     "account_a",
		Endpoint:    "/v1/chat/completions",
		InputFileID: "file-input",
		IO:          batchIO,
		Runner:      &echoRunner{},
	})
	require.NoError(t, err)

	final := waitForTerminalBatch(t, service, "account_a", batch.ID)
	require.Equal(t, jobs.JobStateFailed, final.State)
	require.Contains(t, final.Reason, "128 byte bound")
}

// TestABatchHoldsOneOutstandingJobSlot proves the slot claim and its release:
// a bound of one refuses a second submission while the first runs, and the
// first batch's end gives the slot back.
func TestABatchHoldsOneOutstandingJobSlot(t *testing.T) {
	meter, err := limits.NewJobMeter(storage.NewMockStore())
	require.NoError(t, err)
	service := newBatchService(t, jobs.WithBatchJobMeter(meter), jobs.WithBatchConcurrency(1))
	runner := newBlockingRunner()
	batchIO := newMemoryBatchIO("{\"a\":1}\n")

	submission := jobs.BatchSubmission{
		Account:          "account_a",
		Endpoint:         "/v1/chat/completions",
		InputFileID:      "file-input",
		OutstandingBound: 1,
		IO:               batchIO,
		Runner:           runner,
	}
	batch, err := service.Submit(context.Background(), submission)
	require.NoError(t, err)
	<-runner.started

	_, err = service.Submit(context.Background(), submission)
	require.ErrorIs(t, err, limits.ErrTooManyOutstandingJobs)

	close(runner.release)
	waitForTerminalBatch(t, service, "account_a", batch.ID)

	later, err := service.Submit(context.Background(), jobs.BatchSubmission{
		Account:          "account_a",
		Endpoint:         "/v1/chat/completions",
		InputFileID:      "file-input",
		OutstandingBound: 1,
		IO:               newMemoryBatchIO("{\"a\":1}\n"),
		Runner:           &echoRunner{},
	})
	require.NoError(t, err)
	waitForTerminalBatch(t, service, "account_a", later.ID)
}

// TestASubmissionWithoutARunnerLeavesNoRecord holds the claim-first order: a
// submission the service cannot run stores nothing.
func TestASubmissionWithoutARunnerLeavesNoRecord(t *testing.T) {
	service := newBatchService(t)
	_, err := service.Submit(context.Background(), jobs.BatchSubmission{
		Account:     "account_a",
		Endpoint:    "/v1/chat/completions",
		InputFileID: "file-input",
	})
	require.ErrorIs(t, err, jobs.ErrBatchSubmissionIncomplete)
	records, err := service.List(context.Background(), "account_a", 10)
	require.NoError(t, err)
	require.Empty(t, records)
}
