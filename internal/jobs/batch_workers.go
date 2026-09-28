package jobs

import (
	"context"
	"sync"
)

// batchWorkerLifecycle tracks admission through final cleanup. Shutdown stops
// new dispatch but leaves admitted calls and their result writes running.
type batchWorkerLifecycle struct {
	mu           sync.Mutex
	closed       bool
	active       int
	drained      chan struct{}
	dispatch     context.Context
	stopDispatch context.CancelFunc
}

func newBatchWorkerLifecycle() batchWorkerLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return batchWorkerLifecycle{drained: make(chan struct{}), dispatch: ctx, stopDispatch: cancel}
}
func (w *batchWorkerLifecycle) start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrServiceClosed
	}
	w.active++
	return nil
}
func (w *batchWorkerLifecycle) finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active--
	if w.closed && w.active == 0 {
		close(w.drained)
	}
}

// Close rejects new batches, stops later line dispatch, and waits for cleanup.
// A deadline leaves dependencies in use. The caller must retain them and retry.
func (s *BatchService) Close(ctx context.Context) error {
	w := &s.workerLife
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		w.stopDispatch()
		if w.active == 0 {
			close(w.drained)
		}
	}
	w.mu.Unlock()
	select {
	case <-w.drained:
		return nil
	default:
	}
	select {
	case <-w.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
