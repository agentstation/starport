package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentstation/starmap"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
)

// Candidate is one effective catalog generation offered for acceptance,
// together with the lease epoch the run that produced it started under.
//
// The epoch travels with the candidate because acceptance happens after route
// validation, which takes time. An instance that loses the lease during that
// work must not advance the shared accepted head, and the epoch is what says
// so.
type Candidate struct {
	// State is the effective generation the connected runtime published.
	State starmap.CatalogState

	// Epoch is the lease epoch the candidate was produced under. Zero means
	// the deployment shares no lease, and every candidate then passes the
	// fence.
	Epoch uint64
}

// Accept advances the accepted head to one validated candidate.
//
// The transaction has three parts, in this order. The lease epoch fences the
// write, so a run that lost the lease writes nothing. The candidate must move
// the head forward, so a stale or contradictory generation is refused. The
// write itself is a compare-and-swap against the head the caller read, so two
// instances that reach this point together produce exactly one advance.
//
// Repeating a candidate that the head already carries is a success that writes
// nothing. Shared storage stays idempotent for a retry after a partial
// failure.
func (r *Runtime) Accept(ctx context.Context, candidate Candidate) error {
	if r == nil || r.candidates == nil || r.accepted == nil {
		return ErrCatalogSourceRequired
	}
	if ctx == nil {
		return errors.New("catalog acceptance context is required")
	}
	state := candidate.State
	if state.Catalog == nil || state.GenerationID == "" {
		return ErrCatalogRequired
	}
	if err := r.fenceEpoch(ctx, candidate.Epoch); err != nil {
		return err
	}
	generation, err := r.candidates.Get(ctx, state.GenerationID)
	if err != nil {
		return fmt.Errorf("read candidate catalog generation for acceptance: %w", err)
	}
	if generation.Manifest.Payload.Checksum != state.PayloadChecksum {
		return &starmaperrors.ConflictError{
			Resource: "candidate catalog generation",
			Expected: state.PayloadChecksum,
			Actual:   generation.Manifest.Payload.Checksum,
			Message:  "the atomic state does not match the durable generation",
		}
	}
	if !generation.Manifest.GeneratedAt.Equal(state.GeneratedAt) {
		return &starmaperrors.ConflictError{
			Resource: "candidate catalog generation timestamp",
			Expected: state.GeneratedAt.Format(time.RFC3339Nano),
			Actual:   generation.Manifest.GeneratedAt.Format(time.RFC3339Nano),
		}
	}

	expectedID := ""
	current, currentErr := r.accepted.Current(ctx)
	switch {
	case currentErr == nil:
		expectedID = current.Manifest.GenerationID
		if expectedID == state.GenerationID {
			return nil
		}
		if generation.Manifest.GeneratedAt.Before(current.Manifest.GeneratedAt) {
			return &starmaperrors.ConflictError{
				Resource: "accepted catalog generation order",
				Expected: current.Manifest.GeneratedAt.Format(time.RFC3339Nano),
				Actual:   generation.Manifest.GeneratedAt.Format(time.RFC3339Nano),
				Message:  "an accepted generation cannot move backward",
			}
		}
		if generation.Manifest.GeneratedAt.Equal(current.Manifest.GeneratedAt) &&
			generation.Manifest.Payload.Checksum != current.Manifest.Payload.Checksum {
			return &starmaperrors.ConflictError{
				Resource: "accepted catalog generation order",
				Expected: current.Manifest.Payload.Checksum,
				Actual:   generation.Manifest.Payload.Checksum,
				Message:  "distinct payloads cannot share an accepted generation timestamp",
			}
		}
	case notFound(currentErr):
	default:
		return fmt.Errorf("read accepted catalog generation: %w", currentErr)
	}
	if err := r.accepted.Commit(ctx, generation, expectedID); err != nil {
		return fmt.Errorf("accept catalog generation: %w", err)
	}
	return nil
}

// fenceEpoch refuses a candidate that an older lease epoch produced. A
// deployment that keeps no lease reports epoch zero, and the fence then passes
// every candidate.
func (r *Runtime) fenceEpoch(ctx context.Context, epoch uint64) error {
	if r.leases == nil {
		return nil
	}
	current, err := r.leases.CurrentEpoch(ctx)
	if err != nil {
		return err
	}
	if current == 0 {
		return nil
	}
	if epoch < current {
		return &staleLeaseEpochError{Observed: epoch, Current: current}
	}
	return nil
}

// staleLeaseEpochError reports a candidate that an instance produced before it
// lost the catalog runtime lease.
type staleLeaseEpochError struct {
	// Observed is the epoch the candidate carried.
	Observed uint64
	// Current is the epoch shared storage holds now.
	Current uint64
}

// Error states which epoch the candidate carried and which one is current.
func (e *staleLeaseEpochError) Error() string {
	return fmt.Sprintf(
		"catalog candidate carries lease epoch %d; the current epoch is %d",
		e.Observed, e.Current,
	)
}

// ErrStaleLeaseEpoch matches a candidate the lease fence refused.
var ErrStaleLeaseEpoch = errors.New("catalog candidate carries a stale lease epoch")

// Is reports the sentinel, so a caller matches the refusal without reading the
// epochs.
func (e *staleLeaseEpochError) Is(target error) bool { return target == ErrStaleLeaseEpoch }
