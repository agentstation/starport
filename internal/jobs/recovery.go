package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrRecoveryBusy reports an overlapping sweep on this service.
	ErrRecoveryBusy = errors.New("jobs: recovery sweep already running")
	// ErrSlotReleasePending reports terminal work whose slot release needs retry.
	ErrSlotReleasePending = errors.New("jobs: slot release requires recovery")
)

// RecoveryPage holds one background scan step, including partial read failures.
// A storage failure preserves the input cursor. Record failures advance the
// cursor so a corrupt record cannot prevent other records from recovering.
type RecoveryPage[T any] struct {
	Records []T
	Next    string
	Scanned int
	Failed  int
}

func readRecoveryPage[T any](ctx context.Context, store storage.KVStore, prefix, cursor string, decode func([]byte) (T, error)) (RecoveryPage[T], error) {
	page := RecoveryPage[T]{Next: cursor}
	keys, err := store.ScanPage(ctx, prefix, cursor, 256)
	if err != nil {
		return page, err
	}
	page.Next = keys.Next
	page.Scanned = len(keys.Keys)
	var first error
	for _, key := range keys.Keys {
		data, err := store.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		var record T
		if err == nil {
			record, err = decode(data)
		}
		if err != nil {
			page.Failed++
			if first == nil {
				first = fmt.Errorf("jobs: read recovery record %q: %w", key, err)
			}
			continue
		}
		page.Records = append(page.Records, record)
	}
	return page, first
}

func (r *repository) RecoveryPage(ctx context.Context, cursor string) (RecoveryPage[Job], error) {
	return readRecoveryPage(ctx, r.store, StoragePrefix, cursor, decodeJob)
}

func (r *batchRepository) RecoveryPage(ctx context.Context, cursor string) (RecoveryPage[Batch], error) {
	return readRecoveryPage(ctx, r.store, BatchStoragePrefix, cursor, decodeBatch)
}

// recoveryState retains unprocessed records when the invocation times out.
// The cursor is process-local. A restart safely repeats earlier records.
type recoveryState[T any] struct {
	mu      sync.Mutex
	cursor  string
	next    string
	loaded  bool
	pending []T
}

// recoverPages limits each invocation by time and retains its continuation.
// Restart replays from the start. Record mutations must therefore be idempotent.
func recoverPages[T any](ctx context.Context, state *recoveryState[T], read func(context.Context, string) (RecoveryPage[T], error), visit func(context.Context, T, *SweepResult) error) (SweepResult, error) {
	if !state.mu.TryLock() {
		return SweepResult{}, ErrRecoveryBusy
	}
	defer state.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result SweepResult
	var first error
	for {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(first, err)
		}
		if !state.loaded {
			page, err := read(ctx, state.cursor)
			if err != nil && first == nil {
				first = err
			}
			result.Scanned += page.Scanned
			result.Failed += page.Failed
			if err != nil && page.Scanned == 0 {
				return result, first
			}
			state.pending, state.next, state.loaded = page.Records, page.Next, true
		}
		for len(state.pending) > 0 {
			if err := ctx.Err(); err != nil {
				return result, errors.Join(first, err)
			}
			if err := visit(ctx, state.pending[0], &result); err != nil {
				result.Failed++
				if first == nil {
					first = err
				}
			}
			var zero T
			state.pending[0] = zero
			state.pending = state.pending[1:]
		}
		state.cursor, state.loaded, state.pending = state.next, false, nil
		if state.cursor == "" {
			return result, first
		}
	}
}

// Sweep releases finished batch claims without a client read.
// A cancelled batch whose lines have not drained retains its claim.
func (s *BatchService) Sweep(ctx context.Context) (SweepResult, error) {
	return recoverPages(ctx, &s.recovery, s.repository.RecoveryPage, func(ctx context.Context, batch Batch, result *SweepResult) error {
		if s.openFiles != nil && !batch.ResultsReleased {
			recovered, err := s.RecoverResults(ctx, batch.Account, batch.ID, s.openFiles(batch))
			if err != nil {
				if errors.Is(err, ErrBatchResultsPending) && !recovered.State.Terminal() {
					return nil
				}
				return err
			}
			batch = recovered
		}
		if !batch.RunFinished || batch.SlotReleased || batch.SlotID == "" || s.meter == nil {
			return nil
		}
		recovered, err := s.Get(ctx, batch.Account, batch.ID)
		if err != nil {
			return err
		}
		if !recovered.SlotReleased {
			return fmt.Errorf("%w: batch %s", ErrSlotReleasePending, batch.ID)
		}
		result.Released++
		return nil
	})
}
