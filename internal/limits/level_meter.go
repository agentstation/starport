package limits

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrCounterRequired reports a meter built without an atomic counter.
	ErrCounterRequired = errors.New("level counter is required")
	// ErrInvalidHolder reports an empty holder identity.
	ErrInvalidHolder = errors.New("level holder is required")
)

// Counter is the atomic counter a level meter reserves against.
//
// The limits package names the primitive it needs rather than importing a
// store, because the limit vocabulary stays a leaf. Any counter whose
// increment and decrement are atomic against concurrent callers satisfies it,
// and the durable key-value store already does.
type Counter interface {
	Increment(ctx context.Context, key string, delta int64) (int64, error)
	Decrement(ctx context.Context, key string, delta int64) (int64, error)
}

// levelMeter is the shared mechanics of a limit that is a level rather than a
// rate. Nothing resets a level at an interval boundary: a claim raises the
// total and a release lowers it, and the total stands until something gives it
// back.
//
// Two limits have that shape, and they differ only in what they count and in
// which error a refusal carries. The reservation order below is the part worth
// writing once, because getting it wrong is invisible until two callers race.
type levelMeter struct {
	counter Counter
	// prefix namespaces one holder's counter inside the key-value store.
	prefix string
	// full is the error a refusal wraps, so a caller can tell which of the two
	// levels refused it without reading a message.
	full error
	// what names the level in a wrapped storage failure, and unit and boundUnit
	// name it in a refusal. English needs both spellings: "3 jobs would pass
	// the 2 job bound".
	what      string
	unit      string
	boundUnit string
}

// reserve claims amount for the holder and reports whether the claim fits
// inside bound. A bound of zero or less leaves the holder unbounded, and the
// meter still tracks the total so a later bound reads a true number.
//
// The claim goes in before the check, and a refusal takes it back out. The
// opposite order would read the total, decide, and then write, and two
// concurrent callers would both read a total that admits them and both write
// past the bound. Increment returns the new total, so the check reads a value
// no other caller can be between.
func (m levelMeter) reserve(ctx context.Context, holder string, amount, bound int64) error {
	if holder == "" {
		return ErrInvalidHolder
	}
	if amount <= 0 {
		return nil
	}
	total, err := m.counter.Increment(ctx, m.prefix+holder, amount)
	if err != nil {
		return fmt.Errorf("limits: reserve %s: %w", m.what, err)
	}
	if bound > 0 && total > bound {
		if releaseErr := m.release(ctx, holder, amount); releaseErr != nil {
			// The claim stands. Reporting the refusal alone would hide a total
			// that now counts something nothing holds.
			return errors.Join(m.refusal(total, bound), releaseErr)
		}
		return m.refusal(total, bound)
	}
	return nil
}

// release gives amount back to the holder. The caller uses it for a claim that
// failed and for work that ended and no longer holds what it claimed.
func (m levelMeter) release(ctx context.Context, holder string, amount int64) error {
	if holder == "" {
		return ErrInvalidHolder
	}
	if amount <= 0 {
		return nil
	}
	if _, err := m.counter.Decrement(ctx, m.prefix+holder, amount); err != nil {
		return fmt.Errorf("limits: release %s: %w", m.what, err)
	}
	return nil
}

// total reports what this holder currently holds.
//
// It reads through a zero increment rather than a get, because the counter
// contract owns the encoding of the value and a get would make the caller
// decode it.
func (m levelMeter) total(ctx context.Context, holder string) (int64, error) {
	if holder == "" {
		return 0, ErrInvalidHolder
	}
	total, err := m.counter.Increment(ctx, m.prefix+holder, 0)
	if err != nil {
		return 0, fmt.Errorf("limits: read %s: %w", m.what, err)
	}
	return total, nil
}

func (m levelMeter) refusal(total, bound int64) error {
	return fmt.Errorf("%w: %d %s would pass the %d %s bound",
		m.full, total, m.unit, bound, m.boundUnit)
}
