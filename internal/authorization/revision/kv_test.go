package revision

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
)

func TestKVRevisionCommitsWithMutation(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		revisions := NewKV(store, nil)
		before, err := revisions.Initialize(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := revisions.Apply(t.Context(), []storage.CompareAndSwapMutation{{Key: "policy", NewValue: []byte("first")}}); err != nil {
			t.Fatal(err)
		}
		after, err := revisions.Read(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if after.Epoch != before.Epoch || after.Sequence != before.Sequence+1 {
			t.Fatalf("revision = %+v, before %+v", after, before)
		}
		if err := revisions.Apply(t.Context(), []storage.CompareAndSwapMutation{{Key: "policy", NewValue: []byte("second")}}); !errors.Is(err, storage.ErrConflict) {
			t.Fatalf("conflict = %v", err)
		}
		final, err := revisions.Read(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if final != after {
			t.Fatal("failed mutation advanced revision")
		}
		value, err := store.Get(t.Context(), "policy")
		if err != nil || string(value) != "first" {
			t.Fatalf("policy = %q, %v", value, err)
		}
	})
}

func TestKVRevisionConcurrentIndependentMutations(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		revisions := NewKV(store, nil)
		before, err := revisions.Initialize(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var group sync.WaitGroup
		for i := range 8 {
			group.Go(func() {
				if err := revisions.Apply(t.Context(), []storage.CompareAndSwapMutation{{Key: fmt.Sprintf("policy-%d", i), NewValue: []byte("value")}}); err != nil {
					t.Error(err)
				}
			})
		}
		group.Wait()
		after, err := revisions.Read(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if after.Sequence != before.Sequence+8 || after.Epoch != before.Epoch {
			t.Fatalf("revision = %+v", after)
		}
	})
}

func TestKVRevisionRefusesCorruptionAndOverflow(t *testing.T) {
	for _, data := range []string{"not-json", `{"epoch":"one","sequence":0}`, fmt.Sprintf(`{"epoch":"one","sequence":%d}`, uint64(math.MaxUint64))} {
		store := storage.NewMockStore()
		t.Cleanup(func() { _ = store.Close() })
		if err := store.Set(t.Context(), StorageKey, []byte(data)); err != nil {
			t.Fatal(err)
		}
		if err := NewKV(store, nil).Apply(t.Context(), []storage.CompareAndSwapMutation{{Key: "policy", NewValue: []byte("unsafe")}}); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("corruption = %v", err)
		}
		if _, err := store.Get(t.Context(), "policy"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("partial policy = %v", err)
		}
	}
}

type fencedStore struct {
	storage.KVStore
	fenced *atomic.Bool
	calls  atomic.Int64
}

func (s *fencedStore) CompareAndSwapBatch(ctx context.Context, mutations []storage.CompareAndSwapMutation) error {
	if !s.fenced.Load() {
		return errors.New("mutation reached storage before its local fence")
	}
	s.calls.Add(1)
	return s.KVStore.CompareAndSwapBatch(ctx, mutations)
}

func TestKVRevisionFenceCoversSuccessAndFailure(t *testing.T) {
	var fenced atomic.Bool
	store := &fencedStore{KVStore: storage.NewMockStore(), fenced: &fenced}
	revisions := NewKV(store, func() func() {
		fenced.Store(true)
		return func() { fenced.Store(false) }
	})
	mutation := []storage.CompareAndSwapMutation{{Key: "policy", NewValue: []byte("value")}}
	if err := revisions.Apply(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	if fenced.Load() {
		t.Fatal("successful mutation left fence open")
	}
	if err := revisions.Apply(t.Context(), mutation); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("conflict = %v", err)
	}
	if fenced.Load() {
		t.Fatal("failed mutation left fence open")
	}
	if store.calls.Load() != 2 {
		t.Fatalf("CAS calls = %d", store.calls.Load())
	}
}
