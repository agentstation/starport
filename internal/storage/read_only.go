package storage

import (
	"context"
	"time"
)

type readOnlyStore struct {
	KVStore
}

func (*readOnlyStore) Set(context.Context, string, []byte) error { return ErrReadOnly }

func (*readOnlyStore) Delete(context.Context, string) error { return ErrReadOnly }

func (*readOnlyStore) SetWithTTL(context.Context, string, []byte, time.Duration) error {
	return ErrReadOnly
}

func (*readOnlyStore) ExpireAt(context.Context, string, time.Time) error { return ErrReadOnly }

func (*readOnlyStore) Increment(context.Context, string, int64) (int64, error) {
	return 0, ErrReadOnly
}

func (*readOnlyStore) Decrement(context.Context, string, int64) (int64, error) {
	return 0, ErrReadOnly
}

func (*readOnlyStore) CompareAndSwap(context.Context, string, []byte, []byte) error {
	return ErrReadOnly
}

func (*readOnlyStore) CompareAndSwapBatch(context.Context, []CompareAndSwapMutation) error {
	return ErrReadOnly
}

func (*readOnlyStore) BatchSet(context.Context, map[string][]byte) error { return ErrReadOnly }

func (*readOnlyStore) BatchDelete(context.Context, []string) error { return ErrReadOnly }

func (*readOnlyStore) BatchSetWithTTL(context.Context, map[string][]byte, time.Duration) error {
	return ErrReadOnly
}

func (s *readOnlyStore) Ping(ctx context.Context) error { return s.KVStore.Ping(ctx) }

// readOnlyIncarnationProvider retains shared identity checks without exposing mutations.
type readOnlyIncarnationProvider struct {
	*readOnlyStore
	provider IncarnationProvider
}

func (s *readOnlyIncarnationProvider) ObserveIncarnation(ctx context.Context) (string, error) {
	return s.provider.ObserveIncarnation(ctx)
}

func (s *readOnlyIncarnationProvider) BindIncarnation(ctx context.Context, approved string) (IncarnationStore, error) {
	bound, err := s.provider.BindIncarnation(ctx, approved)
	if err != nil {
		return nil, err
	}
	return &readOnlyIncarnationStore{IncarnationStore: bound}, nil
}

type readOnlyIncarnationStore struct{ IncarnationStore }

func (*readOnlyIncarnationStore) CompareAndSwap(context.Context, []CompareAndSwapMutation, ...string) error {
	return ErrReadOnly
}
