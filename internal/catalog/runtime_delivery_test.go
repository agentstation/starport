package catalog

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type deliveryLeaseStore struct {
	storage.KVStore
	unavailable atomic.Bool
	reads       atomic.Int64
}

func (s *deliveryLeaseStore) Get(ctx context.Context, key string) ([]byte, error) {
	if key == leaseKey {
		s.reads.Add(1)
		if s.unavailable.Load() {
			return nil, errors.New("lease temporarily unavailable")
		}
	}
	return s.KVStore.Get(ctx, key)
}

func TestStandaloneCandidateRetriesPreparationWithoutAnotherPublication(t *testing.T) {
	for _, supersede := range []bool{false, true} {
		name := "same-publication"
		if supersede {
			name = "newer-publication"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				generation := runtimeTestGeneration(t, "delivery", testEmptyCatalog(t, "provider"), time.Now().UTC())
				connected, accepted := acceptanceTestRuntime(t, generation)
				store := &deliveryLeaseStore{KVStore: connected.leases.store}
				connected.leases.store = store
				store.unavailable.Store(true)
				updates := make(chan starmap.CatalogState)
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() {
					defer close(done)
					connected.forwardFrom(ctx, updates)
				}()
				defer func() { cancel(); <-done }()
				updates <- runtimeTestState(t, generation)
				synctest.Wait()
				require.Equal(t, int64(1), store.reads.Load())
				require.Empty(t, connected.updates, "an unknown lease cannot produce a candidate")
				require.Equal(t, RouteValidationPending, connected.RouteValidation().State)
				_, err := accepted.Current(t.Context())
				require.Error(t, err, "delivery failure cannot move the accepted head")
				time.Sleep(31 * time.Second)
				synctest.Wait()
				require.Equal(t, int64(2), store.reads.Load())
				require.Empty(t, connected.updates, "repeated preparation failure must not grant acceptance")
				require.Equal(t, RouteValidationPending, connected.RouteValidation().State)
				if supersede {
					previous := generation.Manifest.GenerationID
					generation = runtimeTestGeneration(t, "newer-delivery", testEmptyCatalog(t, "newer-provider"), time.Now().UTC())
					require.NoError(t, connected.candidates.Commit(t.Context(), generation, previous))
					updates <- runtimeTestState(t, generation)
					synctest.Wait()
					require.Empty(t, connected.updates, "newer pending state cannot bypass preparation")
				}
				store.unavailable.Store(false)
				time.Sleep(31 * time.Second)
				synctest.Wait()
				select {
				case candidate := <-connected.Updates():
					require.Equal(t, generation.Manifest.GenerationID, candidate.State.GenerationID)
					require.NoError(t, connected.Accept(t.Context(), candidate))
				default:
					t.Fatal("candidate disappeared after a transient epoch read failure")
				}
				before := store.reads.Load()
				time.Sleep(time.Minute)
				synctest.Wait()
				require.Equal(t, before, store.reads.Load(), "idle standalone forwarding must not poll storage")
				require.Empty(t, connected.updates, "successful delivery must not repeat")
			})
		})
	}
}
