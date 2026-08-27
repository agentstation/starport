// Package blob stores opaque byte objects under opaque keys.
//
// It is the seam for a value too large to hold in memory or to move through a
// transaction. The package internal/storage owns the other kind: a small
// record that changes, with transactions, batches, and compare-and-set. A
// multi-megabyte value would move through every one of those operations, so
// file bytes get their own contract.
//
// The contract is deliberately narrow. A store knows nothing about tenants,
// files, purposes, or expiry. It reads and writes bytes at a key. The owner of
// the key holds every meaning the bytes carry.
package blob

import (
	"context"
	"errors"
	"io"
)

// Errors the contract defines. A backend returns these rather than an error of
// its own, so a caller can branch on the reason without naming a backend.
var (
	// ErrInvalidKey reports a key the contract refuses. A store returns it
	// before it touches its backing medium, so a rejected key never leaves a
	// partial object behind.
	ErrInvalidKey = errors.New("blob: invalid key")

	// ErrNotFound reports that no object exists at the key.
	ErrNotFound = errors.New("blob: object not found")
)

// Info describes one stored object.
type Info struct {
	// Key is the key the object is stored under.
	Key string

	// Size is the stored length in bytes.
	Size int64
}

// Store reads and writes opaque bytes at an opaque key.
//
// Every method takes a context and honors its cancellation. A backend that
// reaches a network respects the deadline the caller sets.
type Store interface {
	// Put stores the bytes the reader yields at the key. It reads until the
	// reader reports io.EOF.
	//
	// A put that fails leaves no readable object at a key that held none
	// before, and leaves the prior object intact at a key that did. A backend
	// therefore stages the bytes and makes them reachable in one final step.
	//
	// Put returns ErrInvalidKey before it writes anything when the key breaks
	// the key rules.
	Put(ctx context.Context, key string, r io.Reader) (Info, error)

	// Get opens the stored object for reading. The caller closes the reader.
	// Get returns ErrNotFound when no object exists at the key.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Stat reports the object without reading its bytes. Stat returns
	// ErrNotFound when no object exists at the key.
	Stat(ctx context.Context, key string) (Info, error)

	// Delete removes the object. Delete of an absent key returns nil, so a
	// repeated delete is safe.
	Delete(ctx context.Context, key string) error

	// Backend names the implementation, such as "filesystem". An operator
	// reads it once at startup to confirm where the bytes land.
	Backend() string
}
