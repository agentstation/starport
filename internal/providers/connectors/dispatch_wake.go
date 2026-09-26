package connectors

import (
	"sync"
	"sync/atomic"
)

// dispatchWake broadcasts capacity changes without allocating when no requests wait.
type dispatchWake struct {
	mu       sync.Mutex
	revision atomic.Uint64
	changed  chan struct{}
}

func (w *dispatchWake) signal() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.revision.Add(1)
	if w.changed != nil {
		close(w.changed)
		w.changed = nil
	}
}

func (w *dispatchWake) subscribe(revision uint64) <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.revision.Load() != revision {
		return nil
	}
	if w.changed == nil {
		w.changed = make(chan struct{})
	}
	return w.changed
}
