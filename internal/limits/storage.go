//nolint:dupl // StorageMeter repeats JobMeter on purpose. See JobMeter.
package limits

import (
	"context"
	"errors"
)

const (
	// StoredBytesSchemaVersion identifies the only stored byte schema.
	StoredBytesSchemaVersion = 1
	// StoredBytesPrefix is the stored byte v1 namespace.
	StoredBytesPrefix = "limits:v1:stored_bytes:"
)

// ErrStorageFull reports a reservation that would put a holder past its
// stored byte bound.
var ErrStorageFull = errors.New("stored bytes limit exceeded")

// StorageMeter bounds how many bytes one holder keeps at a time.
//
// Stored bytes are a level, not a rate. Nothing resets the total at an
// interval boundary, so the meter tracks a standing amount that a write
// raises and a delete lowers.
type StorageMeter struct {
	level levelMeter
}

// NewStorageMeter builds a meter over an atomic counter.
func NewStorageMeter(counter Counter) (*StorageMeter, error) {
	if counter == nil {
		return nil, ErrCounterRequired
	}
	return &StorageMeter{level: levelMeter{
		counter:   counter,
		prefix:    StoredBytesPrefix,
		full:      ErrStorageFull,
		what:      "stored bytes",
		unit:      "bytes",
		boundUnit: "byte",
	}}, nil
}

// Reserve claims size bytes for the holder and reports whether the claim fits
// inside bound. A bound of zero or less leaves the holder unbounded.
func (m *StorageMeter) Reserve(ctx context.Context, holder string, size, bound int64) error {
	return m.level.reserve(ctx, holder, size, bound)
}

// Release gives size bytes back to the holder. The caller uses it for a write
// that failed and for a delete that removed bytes the meter had counted.
func (m *StorageMeter) Release(ctx context.Context, holder string, size int64) error {
	return m.level.release(ctx, holder, size)
}

// Total reports the bytes this holder currently keeps.
func (m *StorageMeter) Total(ctx context.Context, holder string) (int64, error) {
	return m.level.total(ctx, holder)
}
