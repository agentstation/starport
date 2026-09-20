package cache

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	fillQueueEntries  = 1024
	fillQueueBytes    = 4 << 20
	fillWorkers       = 2
	fillWriteTimeout  = 500 * time.Millisecond
	remoteReadTimeout = 2 * time.Millisecond
)

type fillJob struct {
	store    ResponseStore
	key      string
	value    []byte
	deadline time.Time
	cost     int64
}

// fillQueue bounds queued and active payloads together.
type fillQueue struct {
	store     ResponseStore
	jobs      chan fillJob
	ctx       context.Context
	cancel    context.CancelFunc
	workers   sync.WaitGroup
	mu        sync.Mutex
	closed    bool
	entries   int64
	bytes     int64
	active    int64
	dropped   atomic.Uint64
	failed    atomic.Uint64
	completed atomic.Uint64
}

func newFillQueue(store ResponseStore) *fillQueue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &fillQueue{store: store, jobs: make(chan fillJob, fillQueueEntries), ctx: ctx, cancel: cancel}
	for range fillWorkers {
		q.workers.Go(q.run)
	}
	return q
}

// enqueue drops optional work when capacity or the short admission lock is unavailable.
func (q *fillQueue) enqueue(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return q.enqueueTo(ctx, q.store, key, value, ttl)
}

func (q *fillQueue) enqueueTo(ctx context.Context, store ResponseStore, key string, value []byte, ttl time.Duration) error {
	return q.admit(ctx, store, key, value, ttl, false)
}

// enqueueOwnedTo transfers a freshly encoded buffer without a second payload copy.
func (q *fillQueue) enqueueOwnedTo(ctx context.Context, store ResponseStore, key string, value []byte, ttl time.Duration) error {
	return q.admit(ctx, store, key, value, ttl, true)
}

func (q *fillQueue) admit(ctx context.Context, store ResponseStore, key string, value []byte, ttl time.Duration, owned bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(value) > fillQueueBytes || len(key) > fillQueueBytes-len(value) {
		q.dropped.Add(1)
		return nil
	}
	valueCost := len(value)
	if owned {
		valueCost = cap(value)
	}
	if valueCost > fillQueueBytes-len(key) {
		q.dropped.Add(1)
		return nil
	}
	cost := int64(len(key)+valueCost) + 96
	if !q.mu.TryLock() {
		q.dropped.Add(1)
		return nil
	}
	defer q.mu.Unlock()
	if q.closed {
		return ErrCacheClosed
	}
	if q.entries == fillQueueEntries || cost > fillQueueBytes-q.bytes {
		q.dropped.Add(1)
		return nil
	}
	if ttl <= 0 {
		q.dropped.Add(1)
		return nil
	}
	if !owned {
		value = bytes.Clone(value)
	}
	job := fillJob{store: store, key: strings.Clone(key), value: value, deadline: time.Now().Add(ttl), cost: cost}
	q.entries++
	q.bytes += cost
	select {
	case q.jobs <- job:
		return nil
	default:
		q.entries--
		q.bytes -= cost
		q.dropped.Add(1)
		return nil
	}
}

func (q *fillQueue) run() {
	for {
		if q.ctx.Err() != nil {
			return
		}
		select {
		case <-q.ctx.Done():
			return
		case job := <-q.jobs:
			q.mu.Lock()
			q.active++
			q.mu.Unlock()
			ttl := time.Until(job.deadline)
			if ttl > 0 && q.ctx.Err() == nil {
				ctx, cancel := context.WithTimeout(q.ctx, min(ttl, fillWriteTimeout))
				err := job.store.Set(ctx, job.key, job.value, ttl)
				cancel()
				if err != nil {
					q.failed.Add(1)
				} else {
					q.completed.Add(1)
				}
			} else {
				q.dropped.Add(1)
			}
			q.mu.Lock()
			q.active--
			q.entries--
			q.bytes -= job.cost
			q.mu.Unlock()
		}
	}
}

func (q *fillQueue) stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{RetainedEntries: q.entries, RetainedBytes: q.bytes, ActiveFills: q.active,
		DroppedFills: q.dropped.Load(), FailedFills: q.failed.Load(), CompletedFills: q.completed.Load()}
}

func (q *fillQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cancel()
	q.workers.Wait()
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.jobs) > 0 {
		job := <-q.jobs
		q.entries--
		q.bytes -= job.cost
		q.dropped.Add(1)
	}
}
