package authorization

import (
	"context"
	"errors"
	"sync"
	"time"
)

// WatchedAuthority binds one durable revision source to its configured owner.
// The reader must honor context cancellation.
type WatchedAuthority struct {
	Authority string
	Reader    RevisionReader
}

// AuthorityStatus reports this replica's observation, not fleet-wide enforcement.
// Errors use fixed codes. Storage errors and connection details never enter status.
type AuthorityStatus struct {
	Authority  string
	Epoch      string
	Sequence   uint64
	CheckedAt  time.Time
	VerifiedAt time.Time
	Attempts   uint64
	Failure    string
}

// Monitor checks durable revisions independently of requests and notifications.
// It never extends an existing permission receipt.
type Monitor struct {
	mu                sync.Mutex
	owners            []WatchedAuthority
	authorities       *AuthoritySet
	interval, timeout time.Duration
	status            []AuthorityStatus
	cancel            context.CancelFunc
	done              chan struct{}
	closed            bool
}

// NewMonitor validates its fixed authority set without reading storage or starting work.
func NewMonitor(owners []WatchedAuthority, authorities *AuthoritySet, interval, timeout time.Duration) (*Monitor, error) {
	if authorities == nil || len(owners) != len(authorities.fences) || len(owners) == 0 || interval <= 0 || timeout <= 0 {
		return nil, ErrEvidence
	}
	seen := make(map[string]bool, len(owners))
	for _, owner := range owners {
		if owner.Reader == nil || seen[owner.Authority] {
			return nil, ErrEvidence
		}
		found := false
		for _, fence := range authorities.fences {
			if fence.authority == owner.Authority {
				found = true
			}
		}
		if !found {
			return nil, ErrEvidence
		}
		seen[owner.Authority] = true
	}
	m := &Monitor{owners: append([]WatchedAuthority(nil), owners...), authorities: authorities, interval: interval, timeout: timeout, status: make([]AuthorityStatus, len(owners))}
	for i, owner := range owners {
		m.status[i].Authority = owner.Authority
	}
	return m, nil
}

// Start starts one bounded worker per authority. Repeated calls start no extra work.
func (m *Monitor) Start(parent context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.done = make(chan struct{})
	var group sync.WaitGroup
	for i := range m.owners {
		group.Go(func() { m.run(ctx, i) })
	}
	go func() { group.Wait(); close(m.done) }()
}

func (m *Monitor) run(ctx context.Context, index int) {
	for {
		if ctx.Err() != nil {
			return
		}
		m.check(ctx, index)
		timer := time.NewTimer(m.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *Monitor) check(parent context.Context, index int) {
	ctx, cancel := context.WithTimeout(parent, m.timeout)
	defer cancel()
	owner := m.owners[index]
	stamp, err := owner.Reader.Read(ctx)
	if err == nil && (stamp.Epoch == "" || len(stamp.Epoch) > 256 || stamp.Sequence == 0) {
		err = ErrEvidence
	}
	if err == nil {
		err = m.authorities.Observe(Evidence{Authority: owner.Authority, Epoch: stamp.Epoch, Sequence: stamp.Sequence})
		if err == nil {
			for _, fence := range m.authorities.fences {
				if fence.authority != owner.Authority {
					continue
				}
				current := fence.state.Load()
				if current.blocked {
					err = ErrWithdrawn
				} else if stamp.Sequence < current.sequence {
					err = ErrEvidence
				}
			}
		}
	}
	// A late successful read still establishes a withdrawal. It cannot renew validity.
	err = errors.Join(err, ctx.Err())
	m.mu.Lock()
	defer m.mu.Unlock()
	status := &m.status[index]
	status.CheckedAt = time.Now()
	status.Attempts++
	switch {
	case err == nil:
		status.Epoch, status.Sequence = stamp.Epoch, stamp.Sequence
		status.VerifiedAt = status.CheckedAt
		status.Failure = ""
	case errors.Is(err, ErrWithdrawn):
		status.Failure = "epoch_changed"
	case errors.Is(err, ErrEvidence):
		status.Failure = "invalid_revision"
	case errors.Is(err, context.Canceled):
		status.Failure = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		status.Failure = "deadline"
	default:
		status.Failure = "unavailable"
	}
}

// Status copies observation data without storage reads or evidence renewal.
func (m *Monitor) Status() []AuthorityStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuthorityStatus(nil), m.status...)
}

// Close cancels and joins workers. Callers can retry after a timeout.
func (m *Monitor) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	done := m.done
	m.mu.Unlock()
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
