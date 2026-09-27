package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/storage"
)

// The job and batch stores share the prefix reader below.
// Batch replacement checks state transitions here. Job replacement also binds
// the caller's observed record in repository_store.go.

// readRecordsUnder reads and decodes every record under one prefix. A record
// deleted between the scan and the read is skipped, not an error: a listing
// and a delete run at the same time.
func readRecordsUnder[T any](
	ctx context.Context,
	store storage.KVStore,
	prefix string,
	limit int,
	decode func([]byte) (T, error),
	what string,
) ([]T, error) {
	keys, err := store.ScanWithPrefix(ctx, prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("jobs: scan %s records: %w", what, err)
	}
	records := make([]T, 0, len(keys))
	for _, key := range keys {
		data, err := store.Get(ctx, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("jobs: read listed %s record: %w", what, err)
		}
		record, err := decode(data)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// replaceRecord writes a record that already exists, and it is the point at
// which a state change meets the one transition table. A caller that
// assembled an illegal record fails here rather than in storage, so no store
// has to know which state changes this package allows.
func replaceRecord(
	ctx context.Context,
	store storage.KVStore,
	key string,
	data []byte,
	decodeState func([]byte) (JobState, error),
	next JobState,
	notFound error,
	what string,
) error {
	current, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return notFound
		}
		return fmt.Errorf("jobs: read %s record for replace: %w", what, err)
	}
	stored, err := decodeState(current)
	if err != nil {
		return err
	}
	if stored != next && !CanTransition(stored, next) {
		return fmt.Errorf("%w: %q to %q", ErrIllegalTransition, stored, next)
	}
	if err := store.CompareAndSwap(ctx, key, current, data); err != nil {
		return fmt.Errorf("jobs: replace %s record: %w", what, err)
	}
	return nil
}
