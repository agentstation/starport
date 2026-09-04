package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/google/uuid"
)

// DefaultRetained is how many closed operations the registry keeps. An
// operator reads the recent history of catalog work, and an unbounded history
// would grow with every refresh a scheduler starts.
const DefaultRetained = 64

// OperationKind names the work an operation performs. The set is closed, so an
// audit subject and a metric label read from a fixed vocabulary.
type OperationKind string

// KindCatalogUpdate is the one kind this gateway runs: read the source,
// observe the providers, and offer the result for acceptance.
const KindCatalogUpdate OperationKind = "catalog_update"

// OperationState names where one operation stands. The set is closed.
type OperationState string

const (
	// OperationAccepted means the registry took the request and no run
	// started yet.
	OperationAccepted OperationState = "accepted"
	// OperationRunning means the work is in flight.
	OperationRunning OperationState = "running"
	// OperationSucceeded means the work finished with no failure.
	OperationSucceeded OperationState = "succeeded"
	// OperationFailed means the work ended with a failure.
	OperationFailed OperationState = "failed"
	// OperationCanceled means an operator or a shutdown ended the work.
	OperationCanceled OperationState = "canceled"
)

// OperationReason is the safe cause of a closed operation. The set is closed,
// because this value reaches a log line, a metric label, and an operator
// response, and a provider message must reach none of them.
type OperationReason string

const (
	// ReasonNone is the reason of an operation that did not fail.
	ReasonNone OperationReason = ""
	// ReasonSourceUnavailable means the catalog source answered nothing
	// usable.
	ReasonSourceUnavailable OperationReason = "source_unavailable"
	// ReasonTimedOut means the operation reached its bound.
	ReasonTimedOut OperationReason = "timed_out"
	// ReasonCanceled means an operator or a shutdown ended the operation.
	ReasonCanceled OperationReason = "canceled"
	// ReasonStaleLeaseEpoch means the instance lost the runtime lease while
	// it validated the candidate.
	ReasonStaleLeaseEpoch OperationReason = "stale_lease_epoch"
	// ReasonRouteValidationFailed means the candidate did not become
	// routable.
	ReasonRouteValidationFailed OperationReason = "route_validation_failed"
	// ReasonAcceptedHeadConflict means another instance moved the accepted
	// head first.
	ReasonAcceptedHeadConflict OperationReason = "accepted_head_conflict"
	// ReasonCatalogUnavailable means no catalog generation is present.
	ReasonCatalogUnavailable OperationReason = "catalog_unavailable"
	// ReasonInternalError is the reason of a failure with no safe cause of
	// its own. It never carries the failure text.
	ReasonInternalError OperationReason = "internal_error"
)

// Operation is one unit of catalog work an operator asked for or a schedule
// started. It names what the work is, where it stands, and why it closed. It
// never carries the text of a failure.
type Operation struct {
	// ID identifies the operation on the admin surface.
	ID string `json:"id"`
	// Kind names the work.
	Kind OperationKind `json:"kind"`
	// State is where the operation stands.
	State OperationState `json:"state"`
	// Reason is the safe cause of a closed operation.
	Reason OperationReason `json:"reason,omitempty"`
	// AcceptedAt is when the registry took the request.
	AcceptedAt time.Time `json:"accepted_at"`
	// StartedAt is when the work began.
	StartedAt time.Time `json:"started_at,omitzero"`
	// CompletedAt is when the work closed.
	CompletedAt time.Time `json:"completed_at,omitzero"`
	// GenerationID is the generation the work produced, when it produced one.
	GenerationID string `json:"generation_id,omitempty"`
	// Changed reports whether the work moved the accepted head.
	Changed bool `json:"changed"`
}

// Open reports whether the operation can still change.
func (o Operation) Open() bool {
	return o.State == OperationAccepted || o.State == OperationRunning
}

// OperationResult is what one unit of catalog work produced.
type OperationResult struct {
	// GenerationID is the generation the work produced.
	GenerationID string
	// Changed reports whether the work moved the accepted head.
	Changed bool
}

// ErrOperationNotFound reports an operation identifier the registry does not
// hold. A closed operation the registry pruned reads the same way.
var ErrOperationNotFound = errors.New("catalog operation is not found")

// OperationWork is one unit of catalog work. The context it receives ends when
// the operation is canceled or reaches its bound.
type OperationWork func(context.Context) (OperationResult, error)

// Operations is the registry of catalog operations. It keeps one open
// operation per kind, so overlapping refresh requests join the run in flight
// rather than starting a second one.
type Operations struct {
	mu       sync.Mutex
	records  map[string]*operationRecord
	order    []string
	open     map[OperationKind]string
	retained int
	timeout  time.Duration
	now      func() time.Time
	newID    func() string
	running  sync.WaitGroup
	closed   bool
}

// operationRecord is one operation with the handle that ends its run.
type operationRecord struct {
	operation Operation
	cancel    context.CancelFunc
}

// OperationOption configures the registry.
type OperationOption func(*Operations)

// WithRetainedOperations sets how many closed operations the registry keeps.
func WithRetainedOperations(retained int) OperationOption {
	return func(o *Operations) {
		if retained > 0 {
			o.retained = retained
		}
	}
}

// WithOperationTimeout bounds one operation. The deployment refresh timeout
// supplies it, and zero adds no cap: the transfer bounds end a transfer that
// stops making progress, and the cancel route and Close end a run an operator
// no longer wants.
func WithOperationTimeout(timeout time.Duration) OperationOption {
	return func(o *Operations) {
		if timeout > 0 {
			o.timeout = timeout
		}
	}
}

// NewOperations creates the catalog operation registry.
func NewOperations(options ...OperationOption) *Operations {
	registry := &Operations{
		records:  map[string]*operationRecord{},
		open:     map[OperationKind]string{},
		retained: DefaultRetained,
		now:      time.Now,
		newID:    uuid.NewString,
	}
	for _, option := range options {
		option(registry)
	}
	return registry
}

// Submit accepts one unit of work and returns the operation that carries it.
//
// A kind that already has an open operation joins that operation: the caller
// reads the identifier of the run in flight and no second run starts. The
// second return value reports the join, so a caller can say which answer it
// gave.
//
// The work runs on a background context. A request that ends does not end the
// operation it started, because the operator asked for the work and not for
// the response.
func (o *Operations) Submit(kind OperationKind, work OperationWork) (Operation, bool) {
	if o == nil {
		return Operation{}, false
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return Operation{}, false
	}
	if id, ok := o.open[kind]; ok {
		record := o.records[id]
		o.mu.Unlock()
		return record.operation, true
	}
	runCtx, cancel := o.runContext()
	operation := Operation{
		ID:         o.newID(),
		Kind:       kind,
		State:      OperationAccepted,
		AcceptedAt: o.now().UTC(),
	}
	o.records[operation.ID] = &operationRecord{operation: operation, cancel: cancel}
	o.order = append(o.order, operation.ID)
	o.open[kind] = operation.ID
	o.running.Add(1)
	o.mu.Unlock()

	go o.run(runCtx, cancel, operation.ID, work)
	return operation, false
}

// runContext builds the lifetime of one operation. The work runs on a
// background context, because the request that started it does not own it. A
// registry with no timeout adds no deadline, and the returned cancel still
// ends the run for the cancel route and for Close.
func (o *Operations) runContext() (context.Context, context.CancelFunc) {
	if o.timeout > 0 {
		return context.WithTimeout(context.Background(), o.timeout)
	}
	return context.WithCancel(context.Background())
}

// run performs one unit of work and closes its operation.
func (o *Operations) run(
	ctx context.Context,
	cancel context.CancelFunc,
	id string,
	work OperationWork,
) {
	defer o.running.Done()
	defer cancel()

	o.mutate(id, func(operation *Operation) {
		operation.State = OperationRunning
		operation.StartedAt = o.now().UTC()
	})
	var (
		result OperationResult
		err    error
	)
	if work != nil {
		result, err = work(ctx)
	}
	o.close(id, result, err)
}

// close writes the terminal state of one operation.
func (o *Operations) close(id string, result OperationResult, failure error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	record, ok := o.records[id]
	if !ok {
		return
	}
	record.operation.CompletedAt = o.now().UTC()
	record.operation.GenerationID = result.GenerationID
	record.operation.Changed = result.Changed
	switch {
	case failure == nil:
		record.operation.State = OperationSucceeded
	case errors.Is(failure, context.Canceled):
		record.operation.State = OperationCanceled
		record.operation.Reason = ReasonCanceled
	default:
		record.operation.State = OperationFailed
		record.operation.Reason = ClassifyOperationFailure(failure)
	}
	if current, open := o.open[record.operation.Kind]; open && current == id {
		delete(o.open, record.operation.Kind)
	}
	o.prune()
}

// mutate applies one change to a held operation.
func (o *Operations) mutate(id string, change func(*Operation)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	record, ok := o.records[id]
	if !ok {
		return
	}
	change(&record.operation)
}

// Get returns one operation by identifier.
func (o *Operations) Get(id string) (Operation, error) {
	if o == nil {
		return Operation{}, ErrOperationNotFound
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	record, ok := o.records[id]
	if !ok {
		return Operation{}, fmt.Errorf("%w: %q", ErrOperationNotFound, id)
	}
	return record.operation, nil
}

// Cancel ends one open operation. Canceling a closed operation returns it
// unchanged, so a repeated cancel is a success that changes nothing.
func (o *Operations) Cancel(id string) (Operation, error) {
	if o == nil {
		return Operation{}, ErrOperationNotFound
	}
	o.mu.Lock()
	record, ok := o.records[id]
	if !ok {
		o.mu.Unlock()
		return Operation{}, fmt.Errorf("%w: %q", ErrOperationNotFound, id)
	}
	if !record.operation.Open() {
		operation := record.operation
		o.mu.Unlock()
		return operation, nil
	}
	cancel := record.cancel
	last := record.operation
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// The run itself writes the terminal state, so the caller reads the
	// accepted cancellation rather than a state the registry guessed. A run
	// that closed and pruned while this call held no lock leaves no record, so
	// the caller reads the last operation this call saw.
	o.mu.Lock()
	defer o.mu.Unlock()
	if current, ok := o.records[id]; ok {
		return current.operation, nil
	}
	return last, nil
}

// List returns every held operation, newest first.
func (o *Operations) List() []Operation {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	operations := make([]Operation, 0, len(o.order))
	for index := len(o.order) - 1; index >= 0; index-- {
		if record, ok := o.records[o.order[index]]; ok {
			operations = append(operations, record.operation)
		}
	}
	return operations
}

// Close ends every open operation and waits for the work to stop.
func (o *Operations) Close() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.closed = true
	cancels := make([]context.CancelFunc, 0, len(o.open))
	for _, id := range o.open {
		if record, ok := o.records[id]; ok && record.cancel != nil {
			cancels = append(cancels, record.cancel)
		}
	}
	o.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	o.running.Wait()
}

// prune drops the oldest closed operations past the retention count. An open
// operation is never pruned, because a caller still holds its identifier.
func (o *Operations) prune() {
	closed := 0
	for _, id := range o.order {
		if record, ok := o.records[id]; ok && !record.operation.Open() {
			closed++
		}
	}
	if closed <= o.retained {
		return
	}
	drop := closed - o.retained
	kept := make([]string, 0, len(o.order))
	for _, id := range o.order {
		record, ok := o.records[id]
		if !ok {
			continue
		}
		if drop > 0 && !record.operation.Open() {
			delete(o.records, id)
			drop--
			continue
		}
		kept = append(kept, id)
	}
	o.order = kept
}

// ClassifyOperationFailure maps one failure onto the closed reason set. A
// cause the set does not name reads as an internal error, so no provider or
// source text reaches a log line, a metric label, or an operator response.
func ClassifyOperationFailure(err error) OperationReason {
	switch {
	case err == nil:
		return ReasonNone
	case errors.Is(err, context.DeadlineExceeded):
		return ReasonTimedOut
	case errors.Is(err, context.Canceled):
		return ReasonCanceled
	case errors.Is(err, ErrStaleLeaseEpoch):
		return ReasonStaleLeaseEpoch
	case errors.Is(err, ErrRouteValidationFailed):
		return ReasonRouteValidationFailed
	case errors.Is(err, ErrCatalogRequired), errors.Is(err, ErrCatalogSourceRequired):
		return ReasonCatalogUnavailable
	}
	var conflict *starmaperrors.ConflictError
	if errors.As(err, &conflict) {
		return ReasonAcceptedHeadConflict
	}
	var notFound *starmaperrors.NotFoundError
	if errors.As(err, &notFound) {
		return ReasonCatalogUnavailable
	}
	var timedOut *starmaperrors.TimeoutError
	if errors.As(err, &timedOut) {
		return ReasonTimedOut
	}
	var (
		syncFailure    *starmaperrors.SyncError
		apiFailure     *starmaperrors.APIError
		authentication *starmaperrors.AuthenticationError
	)
	if errors.As(err, &syncFailure) ||
		errors.As(err, &apiFailure) ||
		errors.As(err, &authentication) {
		return ReasonSourceUnavailable
	}
	return ReasonInternalError
}
