package reservation

import (
	"context"
	"slices"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

// recordSnapshot retains reads for one conditional-write attempt only.
// A conflict requires a new snapshot. Unselected records always use fresh reads.
type recordSnapshot struct {
	storage.TimeBoundStore
	keys   []string
	values []storage.LifetimeValue
}

func (r *Repository) snapshot(ctx context.Context, keys []string) (*Repository, error) {
	values, err := r.store.ReadBatchWithLifetime(ctx, keys, maxRecordSize)
	if err != nil {
		return nil, err
	}
	if len(values) != len(keys) {
		return nil, ErrUnavailable
	}
	return &Repository{store: &recordSnapshot{TimeBoundStore: r.store, keys: keys, values: values}}, nil
}

func (s *recordSnapshot) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	index := slices.Index(s.keys, key)
	if index < 0 {
		return s.TimeBoundStore.ReadWithLifetime(ctx, key, maxBytes)
	}
	value := s.values[index]
	if !value.Found {
		return nil, 0, storage.ErrNotFound
	}
	if len(value.Value) > maxBytes {
		return nil, 0, storage.ErrValueTooLarge
	}
	return value.Value, value.Lifetime, nil
}
