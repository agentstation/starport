package jobs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultBatchConcurrency bounds how many lines run at once. The bound
	// exists so one batch cannot open as many provider calls as it has lines.
	DefaultBatchConcurrency = 4
	// DefaultBatchLineBytes bounds one input line. A line past it fails the
	// whole batch, because a scanner that met it mid-file cannot say what the
	// rest of the file holds.
	DefaultBatchLineBytes = 4 << 20

	// batchReplaceAttempts bounds the retry on a concurrent record write. Two
	// writers touch a batch record: the run loop and a cancel. One retry
	// covers the race, and the margin covers a sweep this package may grow.
	batchReplaceAttempts = 4
)

var (
	// ErrBatchAlreadyEnded reports a cancel asked of a batch that already
	// ended. A terminal state accepts no further change.
	ErrBatchAlreadyEnded = errors.New("jobs: the batch already ended")
	// ErrBatchSubmissionIncomplete reports a submission missing a part the
	// run cannot start without.
	ErrBatchSubmissionIncomplete = errors.New("jobs: incomplete batch submission")
)

// BatchIO is how a batch reaches stored files. The interface lives here and
// the file store lives outside, because this package's imports stop at the
// storage seam and the caller already holds a file service.
type BatchIO interface {
	// OpenInput opens the stored input file for one full read.
	OpenInput(ctx context.Context) (io.ReadCloser, error)
	// StoreOutput stores one result file and answers its file identifier. It
	// reads the content to its end, so the caller streams rather than
	// buffering a whole result file.
	StoreOutput(ctx context.Context, name string, content io.Reader) (string, error)
}

// LineRunner executes one input line. The runner owns the codec and the
// gateway call, so this package never learns what a line says. The answer is
// one encoded result line and which file it belongs in.
type LineRunner interface {
	RunLine(ctx context.Context, number int, line []byte) (result []byte, failed bool)
}

// BatchSubmission is everything a batch needs to start.
type BatchSubmission struct {
	// ID is the batch identifier, or empty to mint one here. A caller whose
	// line runner has to name the batch it runs inside mints the identifier
	// first and submits it, because the runner is built before the record.
	ID          string
	Account     string
	KeyID       string
	Endpoint    string
	InputFileID string
	// OutstandingBound is the submitter's outstanding job limit, or zero for
	// unbounded. A batch holds one slot the way a video job does.
	OutstandingBound int64
	IO               BatchIO
	Runner           LineRunner
}

// BatchService owns the batch lifecycle: the record, the run, and the cancel.
type BatchService struct {
	repository  BatchRepository
	meter       Meter
	now         func() time.Time
	mint        func() string
	concurrency int
	lineBytes   int

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// BatchServiceOption adjusts a batch service.
type BatchServiceOption func(*BatchService)

// WithBatchClock replaces the clock, so a test states its own times.
func WithBatchClock(now func() time.Time) BatchServiceOption {
	return func(s *BatchService) {
		if now != nil {
			s.now = now
		}
	}
}

// WithBatchIdentifiers replaces how a batch identifier is minted.
func WithBatchIdentifiers(mint func() string) BatchServiceOption {
	return func(s *BatchService) {
		if mint != nil {
			s.mint = mint
		}
	}
}

// WithBatchJobMeter installs the outstanding-work meter. A batch draws one
// slot from the same bound a video job draws from, because both are work an
// account started and has not finished paying attention to.
func WithBatchJobMeter(meter Meter) BatchServiceOption {
	return func(s *BatchService) { s.meter = meter }
}

// WithBatchConcurrency replaces the per-batch line concurrency bound.
func WithBatchConcurrency(concurrency int) BatchServiceOption {
	return func(s *BatchService) {
		if concurrency > 0 {
			s.concurrency = concurrency
		}
	}
}

// WithBatchLineBytes replaces the per-line byte bound.
func WithBatchLineBytes(bound int) BatchServiceOption {
	return func(s *BatchService) {
		if bound > 0 {
			s.lineBytes = bound
		}
	}
}

// NewBatchService returns a batch service over the given record repository.
func NewBatchService(repository BatchRepository, options ...BatchServiceOption) (*BatchService, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	service := &BatchService{
		repository:  repository,
		now:         time.Now,
		mint:        NewBatchID,
		concurrency: DefaultBatchConcurrency,
		lineBytes:   DefaultBatchLineBytes,
		cancels:     map[string]context.CancelFunc{},
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// NewBatchID mints one batch identifier.
func NewBatchID() string { return "batch-" + uuid.NewString() }

// Submit records a queued batch and starts its run. The slot claim comes
// first, so a submission past the outstanding bound leaves no record behind.
func (s *BatchService) Submit(ctx context.Context, submission BatchSubmission) (Batch, error) {
	if submission.IO == nil || submission.Runner == nil {
		return Batch{}, fmt.Errorf("%w: it carries no file access or no line runner", ErrBatchSubmissionIncomplete)
	}
	id := strings.TrimSpace(submission.ID)
	if id == "" {
		id = s.mint()
	}
	batch, err := NewBatch(
		id, submission.Account, submission.Endpoint, submission.InputFileID, s.now(),
	)
	if err != nil {
		return Batch{}, err
	}
	batch.KeyID = strings.TrimSpace(submission.KeyID)

	if err := s.reserveBatchSlot(ctx, batch.Account, submission.OutstandingBound); err != nil {
		return Batch{}, err
	}
	if err := s.repository.Create(ctx, batch); err != nil {
		s.releaseBatchSlot(batch.Account)
		return Batch{}, err
	}
	// The batch outlives the request that submitted it, so the run detaches
	// from the request context on purpose. Cancel reaches it through the
	// stored cancel function, not through the submitter's context.
	go s.run(batch, submission.IO, submission.Runner) // #nosec G118 -- detaching is the contract.
	return batch, nil
}

// Get answers one batch record the account owns.
func (s *BatchService) Get(ctx context.Context, account, id string) (Batch, error) {
	return s.repository.Get(ctx, account, id)
}

// List answers the account's batches, newest first.
func (s *BatchService) List(ctx context.Context, account string, limit int) ([]Batch, error) {
	return s.repository.List(ctx, account, limit)
}

// Cancel moves the batch to its terminal cancelled state and stops new lines
// from starting. Lines already running drain, and the run loop attaches their
// results to the cancelled record, so a caller keeps what its money bought.
func (s *BatchService) Cancel(ctx context.Context, account, id string) (Batch, error) {
	batch, err := s.mutate(ctx, account, id, func(b *Batch) error {
		if b.State.Terminal() {
			return fmt.Errorf("%w: it is %s", ErrBatchAlreadyEnded, b.State)
		}
		return b.Transition(JobStateCancelled, s.now())
	})
	if err != nil {
		return batch, err
	}
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return batch, nil
}

// run drives one batch from queued to a terminal state. It runs on its own
// goroutine with its own context, because the batch outlives the request
// that submitted it.
func (s *BatchService) run(batch Batch, batchIO BatchIO, runner LineRunner) {
	defer s.releaseBatchSlot(batch.Account)
	ctx := context.Background()

	// The dispatch context gates new lines and nothing else. A cancel ends
	// it, lines already running keep the background context and drain, and
	// the result files still store what those lines produced.
	dispatchCtx, stopDispatch := context.WithCancel(ctx)
	defer stopDispatch()
	s.mu.Lock()
	s.cancels[batch.ID] = stopDispatch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.cancels, batch.ID)
		s.mu.Unlock()
	}()

	if _, err := s.mutate(ctx, batch.Account, batch.ID, func(b *Batch) error {
		return b.Transition(JobStateRunning, s.now())
	}); err != nil {
		// The one legal reason the move fails is a cancel that beat it. The
		// record is terminal, no line started, and there is nothing to store.
		return
	}

	total, err := s.countLines(ctx, batchIO)
	if err != nil {
		s.finish(ctx, batch, runOutcome{failure: err.Error()})
		return
	}
	if _, err := s.mutate(ctx, batch.Account, batch.ID, func(b *Batch) error {
		b.TotalLines = total
		return nil
	}); err != nil {
		s.finish(ctx, batch, runOutcome{failure: fmt.Sprintf("record the line count: %v", err)})
		return
	}

	outcome := s.runLines(ctx, dispatchCtx, batch, batchIO, runner)
	outcome.total = total
	s.finish(ctx, batch, outcome)
}

// runOutcome is what one run pass hands the finish step.
type runOutcome struct {
	total        int
	completed    int
	failed       int
	outputFileID string
	errorFileID  string
	// failure names why the whole batch failed, or is empty when it did not.
	failure string
}

// countLines reads the input once and counts its request lines. The count
// lands on the record before the run, so a caller polling a running batch
// reads how much work it asked for.
func (s *BatchService) countLines(ctx context.Context, batchIO BatchIO) (int, error) {
	input, err := batchIO.OpenInput(ctx)
	if err != nil {
		return 0, fmt.Errorf("open the input file: %w", err)
	}
	defer func() { _ = input.Close() }()
	scanner := s.newLineScanner(input)
	count := 0
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, s.scanFailure(err)
	}
	return count, nil
}

// newLineScanner builds a scanner whose longest token is exactly the line
// bound. The initial buffer stays at or under the bound, because the scanner
// takes the larger of the two and a bigger buffer would raise the bound.
func (s *BatchService) newLineScanner(input io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, min(64*1024, s.lineBytes)), s.lineBytes)
	return scanner
}

// scanFailure names what stopped a read of the input file.
func (s *BatchService) scanFailure(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("an input line is longer than the %d byte bound", s.lineBytes)
	}
	return fmt.Errorf("read the input file: %w", err)
}

// runLines executes every line and streams the results to stored files.
func (s *BatchService) runLines(
	ctx, dispatchCtx context.Context, batch Batch, batchIO BatchIO, runner LineRunner,
) runOutcome {
	input, err := batchIO.OpenInput(ctx)
	if err != nil {
		return runOutcome{failure: fmt.Sprintf("open the input file: %v", err)}
	}
	defer func() { _ = input.Close() }()

	output := newBatchResultFile(ctx, batchIO, batch.ID+"_output.jsonl")
	errorFile := newBatchResultFile(ctx, batchIO, batch.ID+"_errors.jsonl")

	type lineResult struct {
		body   []byte
		failed bool
	}
	results := make(chan lineResult)
	var workers sync.WaitGroup
	slots := make(chan struct{}, s.concurrency)

	var scanErr error
	go func() {
		defer close(results)
		scanner := s.newLineScanner(input)
		number := 0
	scan:
		for scanner.Scan() {
			if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
				continue
			}
			number++
			line := append([]byte(nil), scanner.Bytes()...)
			// The slot wait and the cancel wait share one select, so a
			// cancel that lands while every slot is busy stops the dispatch
			// rather than queueing behind it.
			select {
			case <-dispatchCtx.Done():
				break scan
			case slots <- struct{}{}:
			}
			if dispatchCtx.Err() != nil {
				<-slots
				break scan
			}
			workers.Add(1)
			go func(number int, line []byte) {
				defer workers.Done()
				defer func() { <-slots }()
				body, failed := runner.RunLine(ctx, number, line)
				results <- lineResult{body: body, failed: failed}
			}(number, line)
		}
		scanErr = scanner.Err()
		workers.Wait()
	}()

	outcome := runOutcome{}
	var writeErr error
	for result := range results {
		destination := output
		if result.failed {
			destination = errorFile
			outcome.failed++
		} else {
			outcome.completed++
		}
		if err := destination.writeLine(result.body); err != nil && writeErr == nil {
			writeErr = err
		}
	}

	outcome.outputFileID, err = output.finish()
	if err != nil && writeErr == nil {
		writeErr = err
	}
	outcome.errorFileID, err = errorFile.finish()
	if err != nil && writeErr == nil {
		writeErr = err
	}

	switch {
	case scanErr != nil:
		outcome.failure = s.scanFailure(scanErr).Error()
	case writeErr != nil:
		outcome.failure = fmt.Sprintf("store a result file: %v", writeErr)
	}
	return outcome
}

// finish lands the outcome on the record. A running record moves to its
// terminal state. A cancelled one keeps its state and gains the counts and
// the files, because the cancel already ended it.
func (s *BatchService) finish(ctx context.Context, batch Batch, outcome runOutcome) {
	_, _ = s.mutate(ctx, batch.Account, batch.ID, func(b *Batch) error {
		if outcome.total > b.TotalLines {
			b.TotalLines = outcome.total
		}
		b.CompletedLines = outcome.completed
		b.FailedLines = outcome.failed
		b.OutputFileID = outcome.outputFileID
		b.ErrorFileID = outcome.errorFileID
		if b.State.Terminal() {
			return nil
		}
		if outcome.failure != "" {
			return b.Fail(outcome.failure, s.now())
		}
		return b.Transition(JobStateCompleted, s.now())
	})
}

// mutate applies one change to the freshest record and writes it back. It
// retries a bounded number of times, because the run loop and a cancel write
// the same record and the compare-and-swap refuses the loser.
func (s *BatchService) mutate(
	ctx context.Context, account, id string, change func(*Batch) error,
) (Batch, error) {
	var lastErr error
	for attempt := 0; attempt < batchReplaceAttempts; attempt++ {
		batch, err := s.repository.Get(ctx, account, id)
		if err != nil {
			return Batch{}, err
		}
		if err := change(&batch); err != nil {
			return batch, err
		}
		if err := s.repository.Replace(ctx, batch); err != nil {
			lastErr = err
			continue
		}
		return batch, nil
	}
	return Batch{}, fmt.Errorf("jobs: write batch record: %w", lastErr)
}

func (s *BatchService) reserveBatchSlot(ctx context.Context, account string, bound int64) error {
	if s.meter == nil || bound <= 0 {
		return nil
	}
	return s.meter.Reserve(ctx, account, 1, bound)
}

func (s *BatchService) releaseBatchSlot(account string) {
	if s.meter == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.meter.Release(ctx, account, 1)
}

// batchResultFile streams result lines into one stored file. The pipe means
// the file store reads bytes as lines land, so a large batch never holds its
// whole result in memory. The file itself is created lazily on the first
// line, so a clean run stores no empty error file.
type batchResultFile struct {
	ctx     context.Context
	batchIO BatchIO
	name    string

	writer *io.PipeWriter
	done   chan storedResult
}

type storedResult struct {
	fileID string
	err    error
}

func newBatchResultFile(ctx context.Context, batchIO BatchIO, name string) *batchResultFile {
	return &batchResultFile{ctx: ctx, batchIO: batchIO, name: name}
}

func (f *batchResultFile) writeLine(line []byte) error {
	if f.writer == nil {
		reader, writer := io.Pipe()
		f.writer = writer
		f.done = make(chan storedResult, 1)
		go func() {
			fileID, err := f.batchIO.StoreOutput(f.ctx, f.name, reader)
			if err != nil {
				// The store stopped reading, so the pipe has to stop
				// accepting, or every later write blocks forever.
				_ = reader.CloseWithError(err)
			}
			f.done <- storedResult{fileID: fileID, err: err}
		}()
	}
	if _, err := f.writer.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// finish closes the stream and answers the stored file identifier, or an
// empty one for a file no line ever reached.
func (f *batchResultFile) finish() (string, error) {
	if f.writer == nil {
		return "", nil
	}
	_ = f.writer.Close()
	result := <-f.done
	return result.fileID, result.err
}
