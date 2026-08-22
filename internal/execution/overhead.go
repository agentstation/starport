package execution

import (
	"context"
	"sync"
	"time"
)

// OverheadTimer measures the latency the gateway itself adds to one
// request: wall time elapsed minus time spent waiting on the upstream
// provider. Attempt callbacks mark upstream intervals; the HTTP layer
// reads the difference when it writes the response.
type OverheadTimer struct {
	start time.Time

	mu       sync.Mutex
	upstream time.Duration
}

type overheadTimerKey struct{}

// WithOverheadTimer starts a timer for one request and stores it in the
// context so attempt callbacks and response writers share it.
func WithOverheadTimer(ctx context.Context) (context.Context, *OverheadTimer) {
	timer := &OverheadTimer{start: time.Now()}
	return context.WithValue(ctx, overheadTimerKey{}, timer), timer
}

// OverheadTimerFrom returns the request's timer, or nil when the request
// did not start one. Every timer method accepts a nil receiver, so call
// sites need no guard.
func OverheadTimerFrom(ctx context.Context) *OverheadTimer {
	timer, _ := ctx.Value(overheadTimerKey{}).(*OverheadTimer)
	return timer
}

// TrackUpstream marks the start of one upstream wait and returns the
// function that ends it.
func (t *OverheadTimer) TrackUpstream() func() {
	if t == nil {
		return func() {}
	}
	start := time.Now()
	return func() {
		elapsed := time.Since(start)
		t.mu.Lock()
		t.upstream += elapsed
		t.mu.Unlock()
	}
}

// OverheadMS reports the gateway-added latency measured so far in whole
// milliseconds. It never reports below zero.
func (t *OverheadTimer) OverheadMS() int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	upstream := t.upstream
	t.mu.Unlock()
	overhead := time.Since(t.start) - upstream
	if overhead < 0 {
		return 0
	}
	return overhead.Milliseconds()
}
