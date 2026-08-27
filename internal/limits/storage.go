package limits

import (
	"context"
	"errors"
	"fmt"
)

const (
	// StoredBytesSchemaVersion identifies the only stored byte schema.
	StoredBytesSchemaVersion = 1
	// StoredBytesPrefix is the stored byte v1 namespace.
	StoredBytesPrefix = "limits:v1:stored_bytes:"
)

var (
	// ErrStorageFull reports a reservation that would put a holder past its
	// stored byte bound.
	ErrStorageFull = errors.New("stored bytes limit exceeded")
	// ErrCounterRequired reports a meter built without an atomic counter.
	ErrCounterRequired = errors.New("stored bytes counter is required")
	// ErrInvalidHolder reports an empty holder identity.
	ErrInvalidHolder = errors.New("stored bytes holder is required")
)

// Counter is the atomic counter a StorageMeter reserves against.
//
// The limits package names the primitive it needs rather than importing a
// store, because the limit vocabulary stays a leaf. Any counter whose
// increment and decrement are atomic against concurrent callers satisfies it,
// and the durable key-value store already does.
type Counter interface {
	Increment(ctx context.Context, key string, delta int64) (int64, error)
	Decrement(ctx context.Context, key string, delta int64) (int64, error)
}

// StorageMeter bounds how many bytes one holder keeps at a time.
//
// Stored bytes are a level, not a rate. Nothing resets the total at an
// interval boundary, so the meter tracks a standing amount that a write
// raises and a delete lowers.
type StorageMeter struct {
	counter Counter
}

// NewStorageMeter builds a meter over an atomic counter.
func NewStorageMeter(counter Counter) (*StorageMeter, error) {
	if counter == nil {
		return nil, ErrCounterRequired
	}
	return &StorageMeter{counter: counter}, nil
}

// Reserve claims size bytes for the holder and reports whether the claim fits
// inside bound. A bound of zero or less leaves the holder unbounded, and the
// meter still tracks the total so a later bound reads a true number.
//
// The claim goes in before the check, and a refusal takes it back out. The
// opposite order would read the total, decide, and then write, and two
// concurrent uploads would both read a total that admits them and both write
// past the bound. Increment returns the new total, so the check reads a value
// no other caller can be between.
func (m *StorageMeter) Reserve(ctx context.Context, holder string, size, bound int64) error {
	if holder == "" {
		return ErrInvalidHolder
	}
	if size <= 0 {
		return nil
	}
	total, err := m.counter.Increment(ctx, StoredBytesPrefix+holder, size)
	if err != nil {
		return fmt.Errorf("limits: reserve stored bytes: %w", err)
	}
	if bound > 0 && total > bound {
		if releaseErr := m.Release(ctx, holder, size); releaseErr != nil {
			// The claim stands. Reporting the refusal alone would hide a
			// total that now counts bytes nothing stored.
			return errors.Join(
				fmt.Errorf("%w: %d bytes would pass the %d byte bound", ErrStorageFull, total, bound),
				releaseErr,
			)
		}
		return fmt.Errorf("%w: %d bytes would pass the %d byte bound", ErrStorageFull, total, bound)
	}
	return nil
}

// Release gives size bytes back to the holder. The caller uses it for a write
// that failed and for a delete that removed bytes the meter had counted.
func (m *StorageMeter) Release(ctx context.Context, holder string, size int64) error {
	if holder == "" {
		return ErrInvalidHolder
	}
	if size <= 0 {
		return nil
	}
	if _, err := m.counter.Decrement(ctx, StoredBytesPrefix+holder, size); err != nil {
		return fmt.Errorf("limits: release stored bytes: %w", err)
	}
	return nil
}

// Total reports the bytes this holder currently keeps.
//
// It reads through a zero increment rather than a get, because the counter
// contract owns the encoding of the value and a get would make the caller
// decode it.
func (m *StorageMeter) Total(ctx context.Context, holder string) (int64, error) {
	if holder == "" {
		return 0, ErrInvalidHolder
	}
	total, err := m.counter.Increment(ctx, StoredBytesPrefix+holder, 0)
	if err != nil {
		return 0, fmt.Errorf("limits: read stored bytes: %w", err)
	}
	return total, nil
}
