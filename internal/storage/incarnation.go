package storage

import (
	"context"
	"errors"
	"time"
)

// ErrIncarnationChanged refuses an operation on an unapproved backend process or role.
var ErrIncarnationChanged = errors.New("storage backend incarnation changed")

// IncarnationStore checks an externally approved backend identity in every operation.
// The caller gets approval from its independent recovery authority.
// Observed identity alone does not grant permission to use a backend.
type IncarnationStore interface {
	ReadWithLifetime(context.Context, string, int) ([]byte, time.Duration, error)
	// CompareAndSwap checks all values and native lease lifetimes before any write.
	// Each live key must occur in mutations with a non-nil ExpectedValue.
	// A live key requires positive native PTTL, including when its value matches.
	CompareAndSwap(context.Context, []CompareAndSwapMutation, ...string) error
}

// IncarnationProvider offers process-bound operations for shared recovery.
// Standalone stores do not need this contract.
type IncarnationProvider interface {
	ObserveIncarnation(context.Context) (string, error)
	BindIncarnation(context.Context, string) (IncarnationStore, error)
}
