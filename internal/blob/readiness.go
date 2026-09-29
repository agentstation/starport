package blob

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrPublicationUnavailable refuses work before it can create provider charges.
var ErrPublicationUnavailable = errors.New("blob: conditional publication is unavailable")

// ErrConditionalPublicationUnsupported reports a failed conditional-write probe.
var ErrConditionalPublicationUnsupported = errors.New("blob: storage did not enforce conditional publication")

// blobReadiness retains capability evidence for one configured client.
// Success is not a promise of future availability or physical data erasure.
type blobReadiness struct {
	ready      atomic.Bool
	mu         sync.Mutex
	running    chan struct{}
	retryAfter time.Time
	last       error
}

func (r *blobReadiness) ensure(ctx context.Context, probe func(context.Context) error, unavailable error) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(unavailable, err)
	}
	if r.ready.Load() {
		return nil
	}
	r.mu.Lock()
	if r.ready.Load() {
		r.mu.Unlock()
		return nil
	}
	if active := r.running; active != nil {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return errors.Join(unavailable, ctx.Err())
		case <-active:
			return r.ensure(ctx, probe, unavailable)
		}
	}
	if time.Now().Before(r.retryAfter) {
		err := r.last
		r.mu.Unlock()
		return err
	}
	active := make(chan struct{})
	r.running = active
	r.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := probe(bounded)
	cancel()
	r.mu.Lock()
	if err == nil {
		r.ready.Store(true)
	} else {
		err = errors.Join(unavailable, err)
		r.retryAfter = time.Now().Add(time.Second)
	}
	r.last = err
	r.running = nil
	close(active)
	r.mu.Unlock()
	return err
}

// EnsurePublicationReady uses the filesystem's qualified native operations.
// Each later write still reports its own permission and storage failures.
func (f *Filesystem) EnsurePublicationReady(ctx context.Context) error { return ctx.Err() }

// EnsurePublicationReady verifies this client's conditional-write support.
// Construction does not contact the bucket. Warm checks read process memory.
func (o *ObjectStore) EnsurePublicationReady(ctx context.Context) error {
	return o.readiness.ensure(ctx, o.probePublication, ErrPublicationUnavailable)
}
