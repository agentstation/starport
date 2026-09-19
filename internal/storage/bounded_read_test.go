package storage_test

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"testing"
)

func TestBoundedStorageRead(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		if err := store.Set(t.Context(), "record", []byte("1234")); err != nil {
			t.Fatal(err)
		}
		value, err := store.GetBounded(t.Context(), "record", 4)
		if err != nil || string(value) != "1234" {
			t.Fatalf("boundary read = %q, %v", value, err)
		}
		value[0] = 'x'
		next, err := store.GetBounded(t.Context(), "record", 4)
		if err != nil || string(next) != "1234" {
			t.Fatalf("caller changed stored bytes: %q, %v", next, err)
		}
		for _, tc := range []struct {
			key   string
			limit int
			want  error
		}{
			{"record", 3, storage.ErrValueTooLarge},
			{"missing", 4, storage.ErrNotFound},
			{"record", 0, storage.ErrInvalidReadLimit},
			{"record", -1, storage.ErrInvalidReadLimit},
		} {
			value, err := store.GetBounded(t.Context(), tc.key, tc.limit)
			if !errors.Is(err, tc.want) || value != nil {
				t.Fatalf("refusal returned data: %q, %v; want %v", value, err, tc.want)
			}
		}
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := store.GetBounded(canceled, "record", 4); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled read = %v", err)
		}
		if err := store.Set(t.Context(), "empty", []byte{}); err != nil {
			t.Fatal(err)
		}
		if value, err := store.GetBounded(t.Context(), "empty", 1); err != nil || len(value) != 0 {
			t.Fatalf("empty = %q, %v", value, err)
		}
	})
}
