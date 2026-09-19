// Package authorization owns the validity of cached caller permissions.
package authorization

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrUnavailable reports an authority or clock that cannot establish permission.
	ErrUnavailable = errors.New("authorization authority unavailable")
	// ErrWithdrawn reports a fence change after a load or receipt.
	ErrWithdrawn = errors.New("authorization changed during use")
	// ErrExpired reports the end of the verified permission interval.
	ErrExpired = errors.New("authorization validity expired")
	// ErrEvidence reports incomplete or incompatible authority evidence.
	ErrEvidence = errors.New("authorization evidence is invalid")
)

// Evidence records a verified authority receipt, not a cache access time.
// The repository adapter must prove its epoch and sequence before publication.
type Evidence struct {
	Authority  string
	Epoch      string
	Sequence   uint64
	VerifiedAt time.Time
	ValidUntil time.Time
}

type fenceState struct {
	blocked   bool
	mutations uint64
	sequence  uint64
}

// Fence prevents publication across local mutations and missed-update recovery.
// One deployment authority owns one fence. It holds no caller records.
type Fence struct {
	authority string
	epoch     string
	mu        sync.Mutex
	state     atomic.Pointer[fenceState]
}

// NewFence creates an authority fence before any callers load state.
func NewFence(authority, epoch string) *Fence {
	f := &Fence{authority: authority, epoch: epoch}
	f.state.Store(&fenceState{sequence: 1})
	return f
}

// Ticket identifies the local authority state before a durable load starts.
type Ticket struct {
	fence *Fence
	state *fenceState
}

// Start refuses loads during a local mutation or recovery operation.
func (f *Fence) Start() (Ticket, error) {
	state := f.state.Load()
	if state == nil || state.mutations != 0 || state.blocked || f.authority == "" || f.epoch == "" {
		return Ticket{}, ErrUnavailable
	}
	return Ticket{fence: f, state: state}, nil
}

// BeginMutation revokes existing receipts before a repository mutation starts.
// Call the returned function after either success or failure. It is idempotent.
// A failed mutation still requires fresh evidence before admission resumes.
func (f *Fence) BeginMutation() func() {
	f.mu.Lock()
	previous := f.state.Load()
	next := &fenceState{mutations: 1, sequence: 1}
	if previous != nil {
		next.mutations = previous.mutations + 1
		next.sequence = previous.sequence
		next.blocked = previous.blocked
	}
	f.state.Store(next)
	f.mu.Unlock()
	return sync.OnceFunc(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		current := f.state.Load()
		f.state.Store(&fenceState{mutations: current.mutations - 1, sequence: current.sequence, blocked: current.blocked})
	})
}

// Require records a verified withdrawal before replacement data can activate.
// The caller must authenticate the sequence within this fence's authority epoch.
func (f *Fence) Require(sequence uint64) error {
	if sequence == 0 {
		return ErrEvidence
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	current := f.state.Load()
	if current == nil {
		return ErrUnavailable
	}
	if sequence > current.sequence {
		f.state.Store(&fenceState{sequence: sequence, mutations: current.mutations, blocked: current.blocked})
	}
	return nil
}

// Receipt permits memory reads only during its original verified interval.
// Copies share the same fence and cannot escape a withdrawal.
type Receipt struct {
	ticket   Ticket
	evidence Evidence
	deadline time.Time
	earliest time.Time
}

// Accept bounds authority evidence by the configured permission lifetime.
// Clock uncertainty shortens validity. Unknown clock health refuses acceptance.
func (t Ticket) Accept(evidence Evidence, now time.Time, maxLifetime, uncertainty time.Duration, clockHealthy bool) (Receipt, error) {
	if !clockHealthy || maxLifetime <= 0 || uncertainty < 0 || uncertainty >= maxLifetime {
		return Receipt{}, ErrUnavailable
	}
	if evidence.Authority == "" || evidence.Epoch == "" || evidence.Sequence == 0 || evidence.VerifiedAt.IsZero() || evidence.ValidUntil.IsZero() {
		return Receipt{}, ErrEvidence
	}
	if evidence.VerifiedAt.After(now.Add(uncertainty)) || !evidence.ValidUntil.After(evidence.VerifiedAt) {
		return Receipt{}, ErrEvidence
	}
	if t.fence == nil || t.state == nil {
		return Receipt{}, ErrEvidence
	}
	if evidence.Authority != t.fence.authority || evidence.Epoch != t.fence.epoch || evidence.Sequence < t.state.sequence {
		return Receipt{}, ErrEvidence
	}
	deadline := evidence.ValidUntil
	limit := evidence.VerifiedAt.Add(maxLifetime)
	if limit.Before(deadline) {
		deadline = limit
	}
	deadline = deadline.Add(-uncertainty)
	receipt := Receipt{ticket: t, evidence: evidence, deadline: deadline, earliest: evidence.VerifiedAt.Add(-uncertainty)}
	if err := receipt.Check(now, clockHealthy); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// Check rechecks permission before admission, retries, and cached response delivery.
// The caller must supply current clock health on every check.
func (r Receipt) Check(now time.Time, clockHealthy bool) error {
	if !clockHealthy || now.Before(r.earliest) {
		return ErrUnavailable
	}
	if r.ticket.fence == nil || r.ticket.state == nil {
		return ErrEvidence
	}
	if r.ticket.fence.state.Load() != r.ticket.state {
		return ErrWithdrawn
	}
	if !now.Before(r.deadline) {
		return ErrExpired
	}
	return nil
}

// Evidence returns the original receipt without extending its validity.
func (r Receipt) Evidence() Evidence { return r.evidence }

// Deadline returns the effective expiry after the clock allowance.
func (r Receipt) Deadline() time.Time { return r.deadline }

// Withdraw closes an authority until explicit recovery constructs a new fence.
func (f *Fence) Withdraw() {
	f.mu.Lock()
	defer f.mu.Unlock()
	current := f.state.Load()
	if current == nil || current.blocked {
		return
	}
	f.state.Store(&fenceState{blocked: true, mutations: current.mutations, sequence: current.sequence})
}
