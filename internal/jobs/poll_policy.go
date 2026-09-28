package jobs

import (
	"fmt"
	"time"
)

// Default polling bounds. A provider that answers in seconds is served by the
// first interval, and one that takes minutes is served by the cap without
// spending a provider request every second while it works.
const (
	// DefaultFirstPoll is the wait before the first poll after a submission.
	DefaultFirstPoll = 2 * time.Second
	// DefaultMaxPoll is the longest wait between two polls.
	DefaultMaxPoll = 30 * time.Second
	// DefaultLifetime bounds automatic polling. Explicit reconciliation can
	// check an unresolved provider outcome after this interval.
	DefaultLifetime = time.Hour
)

// PollPolicy bounds what one job costs in provider requests.
//
// Polling spends the account's own credential on asking rather than on work, so
// two separate bounds apply. Backoff spreads the requests across the wait, and
// Lifetime stops them: without it a provider that never reaches a terminal
// state would leave a job polling for as long as the process runs.
type PollPolicy struct {
	// First is the wait before the first poll.
	First time.Duration
	// Max is the ceiling the wait doubles towards.
	Max time.Duration
	// Lifetime is measured from the job's creation, not from the last poll, so
	// a provider that keeps reporting progress cannot extend it.
	Lifetime time.Duration
}

// DefaultPollPolicy returns the bounds Starport applies when an operator states
// none.
func DefaultPollPolicy() PollPolicy {
	return PollPolicy{
		First:    DefaultFirstPoll,
		Max:      DefaultMaxPoll,
		Lifetime: DefaultLifetime,
	}
}

// Validate reports whether the bounds describe a policy that terminates.
func (p PollPolicy) Validate() error {
	switch {
	case p.First <= 0:
		return fmt.Errorf("%w: the first poll wait is not positive", ErrInvalidJob)
	case p.Max < p.First:
		return fmt.Errorf("%w: the poll ceiling is below the first wait", ErrInvalidJob)
	case p.Lifetime <= 0:
		return fmt.Errorf("%w: the lifetime is not positive", ErrInvalidJob)
	}
	return nil
}

// Backoff returns the suggested delay for a numbered poll.
// Poll zero waits First. Later polls double the delay up to Max.
func (p PollPolicy) Backoff(poll int) time.Duration {
	wait := p.First
	for range poll {
		if wait >= p.Max {
			return p.Max
		}
		wait *= 2
	}
	if wait > p.Max {
		return p.Max
	}
	return wait
}

// Spent reports whether the job has outlived the budget. A terminal job is
// never spent: it already reached its answer, and its record stays readable for
// as long as the retention window AMJ6 states.
func (p PollPolicy) Spent(job Job, now time.Time) bool {
	if job.State.Terminal() {
		return false
	}
	return !now.Before(job.CreatedAt.Add(p.Lifetime))
}
