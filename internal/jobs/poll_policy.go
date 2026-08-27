package jobs

import (
	"errors"
	"fmt"
	"time"
)

// ErrJobLifetimeExceeded reports a job that outlived the budget for polling it.
var ErrJobLifetimeExceeded = errors.New("jobs: job outlived its polling budget")

// Default polling bounds. A provider that answers in seconds is served by the
// first interval, and one that takes minutes is served by the cap without
// spending a provider request every second while it works.
const (
	// DefaultFirstPoll is the wait before the first poll after a submission.
	DefaultFirstPoll = 2 * time.Second
	// DefaultMaxPoll is the longest wait between two polls.
	DefaultMaxPoll = 30 * time.Second
	// DefaultLifetime is the longest a job stays pollable. Past it the job is
	// failed rather than polled again, because a provider that has not
	// answered in an hour is not going to.
	DefaultLifetime = time.Hour
)

// PollPolicy bounds what one job costs in provider requests.
//
// Polling spends the tenant's own credential on asking rather than on work, so
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

// Backoff returns the wait before the poll with this number. The first poll is
// number zero and waits First. Each later poll doubles the wait up to Max, so
// the request count grows with the logarithm of the wait rather than with the
// wait.
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

// FailSpent moves a job that outlived the budget to its terminal failed state
// and states why. It reports ErrJobLifetimeExceeded rather than doing nothing,
// so a caller that asks about a job still inside its budget cannot mistake the
// answer for a job it just ended.
func (p PollPolicy) FailSpent(job *Job, now time.Time) error {
	if job == nil {
		return fmt.Errorf("%w: no record was given", ErrInvalidJob)
	}
	if !p.Spent(*job, now) {
		return fmt.Errorf("%w: %s is inside its budget", ErrJobLifetimeExceeded, job.ID)
	}
	return job.Fail(fmt.Sprintf(
		"the provider did not finish within the %s Starport polls a job for",
		p.Lifetime,
	), now)
}
