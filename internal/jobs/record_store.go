package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/storage"
)

// The job and batch stores share the prefix reader below.
// Each repository binds replacement to the complete caller-observed record.

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
