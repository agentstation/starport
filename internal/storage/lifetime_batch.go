package storage

import (
	"slices"
	"time"
)

const (
	maxLifetimeBatchKeys  = 16
	maxLifetimeBatchBytes = 1 << 20
)

// LifetimeValue holds one snapshot value and its remaining lifetime.
// Found distinguishes a missing record from an existing empty value.
type LifetimeValue struct {
	Value    []byte
	Lifetime time.Duration
	Found    bool
}

// A batch accepts at most sixteen unique keys and one MiB of possible payload.
// maxBytes bounds each value before copying or transfer. Results retain key order.
func validateLifetimeBatch(keys []string, maxBytes int) error {
	if len(keys) == 0 || len(keys) > maxLifetimeBatchKeys || maxBytes <= 0 || maxBytes > maxLifetimeBatchBytes/len(keys) {
		return ErrInvalidReadLimit
	}
	for i, key := range keys {
		if key == "" || slices.Contains(keys[:i], key) {
			return ErrInvalidKey
		}
	}
	return nil
}
