package app

import (
	"context"
	"sync"
)

// advisoryWorkers owns background health and latency exchange.
type advisoryWorkers struct {
	mu      sync.Mutex
	workers []func(context.Context)
	cancel  context.CancelFunc
	done    chan struct{}
	closed  bool
}

func (w *advisoryWorkers) Start(parent context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	w.done = make(chan struct{})
	var group sync.WaitGroup
	for _, worker := range w.workers {
		group.Go(func() { worker(ctx) })
	}
	go func() { group.Wait(); close(w.done) }()
}

func (w *advisoryWorkers) Close(ctx context.Context) error {
	w.mu.Lock()
	w.closed = true
	if w.cancel != nil {
		w.cancel()
	}
	done := w.done
	w.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
