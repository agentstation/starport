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

func (*readOnlyStore) BeginTransaction(context.Context) (Transaction, error) {
	return nil, ErrReadOnly
}

func (s *readOnlyStore) Ping(ctx context.Context) error { return s.KVStore.Ping(ctx) }
