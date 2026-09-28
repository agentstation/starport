package storage

import (
	"context"
	"errors"
	"time"
)

// ErrTimeWindowChanged refuses a write outside its authority-time interval.
var ErrTimeWindowChanged = errors.New("storage authority time left the selected window")

// TimeWindow is a half-open interval in the storage authority's UTC clock.
// Boundaries have whole-second precision. The zero value imposes no time check.
type TimeWindow struct {
	Start time.Time
	End   time.Time
}

func (w TimeWindow) validate() error {
	if w == (TimeWindow{}) {
		return nil
	}
	if w.Start.IsZero() || !w.End.After(w.Start) || w.Start.Nanosecond() != 0 || w.End.Nanosecond() != 0 || w.Start.Year() < 1970 || w.End.Year() > 9999 {
		return ErrInvalidMutation
	}
	return nil
}

func (w TimeWindow) contains(now time.Time) bool {
	return w == (TimeWindow{}) || (!now.Before(w.Start) && now.Before(w.End))
}

// TimeBoundStore uses its own clock to guard conditional writes.
// Reading AuthorityTime does not authorize a later write. The write checks time
// again in the same transaction that checks values. Shared implementations must
// also enforce their approved backend incarnation in each operation.
type TimeBoundStore interface {
	ReadWithLifetime(context.Context, string, int) ([]byte, time.Duration, error)
	// ReadBatchWithLifetime returns one snapshot in key order, with no partial results on error.
	// It accepts at most sixteen unique keys and one MiB of possible payload.
	// The byte bound applies per value. The operation grants no write permission.
	ReadBatchWithLifetime(context.Context, []string, int) ([]LifetimeValue, error)
	AuthorityTime(context.Context) (time.Time, error)
	CompareAndSwapInWindow(context.Context, []CompareAndSwapMutation, TimeWindow) error
}

// AuthorityTime returns host time for the standalone Badger authority.
func (s *BadgerStore) AuthorityTime(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return time.Time{}, ErrStorageClosed
	}
	return time.Now().UTC(), nil
}

// CompareAndSwapInWindow checks the host clock in the Badger transaction.
func (s *BadgerStore) CompareAndSwapInWindow(ctx context.Context, mutations []CompareAndSwapMutation, window TimeWindow) error {
	return s.compareAndSwapBatch(ctx, mutations, window)
}

var _ TimeBoundStore = (*BadgerStore)(nil)
