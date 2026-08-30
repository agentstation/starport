package jobs

// A batch is the second kind of work this package owns that outlives its
// request. A video job hands one unit of work to one provider and polls. A
// batch reads a stored JSONL file and runs every line through the gateway's
// own planner, so it holds counts and file identifiers instead of a provider
// job identifier and an asset.
//
// The batch reuses the job state vocabulary and its one transition table.
// The five words mean the same things, the terminal rule is the same rule,
// and a second table would drift from the first.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrInvalidBatch reports a batch record that cannot be stored as given.
	ErrInvalidBatch = errors.New("jobs: invalid batch")
)

// Batch is one offline run over a stored input file.
type Batch struct {
	ID      string
	Account string
	// KeyID names the gateway API key that submitted the work. It is optional
	// for the reason Job.KeyID is: a deployment with authentication off
	// submits batches no key signed.
	KeyID string
	// Endpoint is the one operation path every line calls, such as
	// "/v1/chat/completions". The line runner has already checked each line
	// against it by the time a result exists.
	Endpoint string
	// InputFileID names the stored JSONL file the batch reads.
	InputFileID string
	// OutputFileID names the stored result file. It is empty until the batch
	// ends, because the file becomes readable only when its bytes finish.
	OutputFileID string
	// ErrorFileID names the stored error file. It is empty when no line
	// failed, so a caller reads its absence as a clean run.
	ErrorFileID string
	State       JobState
	// Reason states why a failed batch stopped before its lines did. A
	// per-line failure is not one: it lands in the error file and the batch
	// completes.
	Reason string
	// TotalLines is how many request lines the input file holds. It is zero
	// until the batch counts them.
	TotalLines int
	// CompletedLines is how many lines produced an output-file result.
	CompletedLines int
	// FailedLines is how many lines produced an error-file entry.
	FailedLines int
	CreatedAt   time.Time
	TerminalAt  time.Time
}

// Validate reports whether the record can be stored.
func (b Batch) Validate() error {
	switch {
	case strings.TrimSpace(b.ID) == "":
		return fmt.Errorf("%w: it has no identifier", ErrInvalidBatch)
	case strings.TrimSpace(b.Account) == "":
		return fmt.Errorf("%w: it names no account", ErrInvalidBatch)
	case strings.TrimSpace(b.Endpoint) == "":
		return fmt.Errorf("%w: it names no endpoint", ErrInvalidBatch)
	case strings.TrimSpace(b.InputFileID) == "":
		return fmt.Errorf("%w: it names no input file", ErrInvalidBatch)
	case b.CreatedAt.IsZero():
		return fmt.Errorf("%w: it has no creation time", ErrInvalidBatch)
	case !b.State.Valid():
		return fmt.Errorf("%w: unknown state %q", ErrInvalidBatch, b.State)
	case b.State.Terminal() && b.TerminalAt.IsZero():
		return fmt.Errorf("%w: state %q records no end time", ErrInvalidBatch, b.State)
	case !b.State.Terminal() && !b.TerminalAt.IsZero():
		return fmt.Errorf("%w: state %q records an end time", ErrInvalidBatch, b.State)
	case b.State == JobStateFailed && strings.TrimSpace(b.Reason) == "":
		return fmt.Errorf("%w: a failed batch states no reason", ErrInvalidBatch)
	case b.State != JobStateFailed && strings.TrimSpace(b.Reason) != "":
		return fmt.Errorf("%w: state %q states a failure reason", ErrInvalidBatch, b.State)
	case b.TotalLines < 0 || b.CompletedLines < 0 || b.FailedLines < 0:
		return fmt.Errorf("%w: it reports a negative line count", ErrInvalidBatch)
	case b.TotalLines > 0 && b.CompletedLines+b.FailedLines > b.TotalLines:
		return fmt.Errorf("%w: it reports more results than lines", ErrInvalidBatch)
	case !b.State.Terminal() && (b.OutputFileID != "" || b.ErrorFileID != ""):
		// The result files finish when the run does. A record that named one
		// earlier would hand out a file identifier the file store cannot
		// serve yet.
		return fmt.Errorf("%w: state %q names a result file", ErrInvalidBatch, b.State)
	}
	return nil
}

// NewBatch returns a queued batch. A batch starts queued because the gateway
// has accepted the work by the time a record exists, the same moment a video
// job starts queued.
func NewBatch(id, account, endpoint, inputFileID string, now time.Time) (Batch, error) {
	batch := Batch{
		ID:          id,
		Account:     account,
		Endpoint:    endpoint,
		InputFileID: inputFileID,
		State:       JobStateQueued,
		CreatedAt:   now,
	}
	if err := batch.Validate(); err != nil {
		return Batch{}, err
	}
	return batch, nil
}

// Transition moves the batch through the one transition table and stamps the
// end of a terminal move. It refuses JobStateFailed for the reason Job's door
// does: that state carries a reason, and FailBatch is the door for it.
func (b *Batch) Transition(to JobState, now time.Time) error {
	if to == JobStateFailed {
		return fmt.Errorf("%w: a failed batch states its reason, so use Fail", ErrInvalidBatch)
	}
	return b.transition(to, now)
}

// Fail moves the batch to its terminal failed state and records why. Every
// path that ends a batch before its lines passes through here: an input file
// that cannot open, a line past the byte bound, and a result file whose write
// broke.
func (b *Batch) Fail(reason string, now time.Time) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: a failed batch states no reason", ErrInvalidBatch)
	}
	if err := b.transition(JobStateFailed, now); err != nil {
		return err
	}
	b.Reason = reason
	return nil
}

func (b *Batch) transition(to JobState, now time.Time) error {
	if !CanTransition(b.State, to) {
		return fmt.Errorf("%w: %q to %q", ErrIllegalTransition, b.State, to)
	}
	b.State = to
	if to.Terminal() {
		b.TerminalAt = now
	}
	return nil
}
