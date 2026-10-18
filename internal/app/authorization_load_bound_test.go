package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
)

// slowRevisionStore stands in for a cold local store on a loaded machine. Once
// armed, it holds the next authorization revision read for the stall, bounded
// by the read's context, and records the bound that the read ran under.
type slowRevisionStore struct {
	storage.KVStore
	bound     storage.TimeBoundStore
	stall     time.Duration
	armed     atomic.Bool
	stalls    atomic.Int64
	remaining atomic.Int64
}

func (s *slowRevisionStore) GetBounded(ctx context.Context, key string, maxBytes int) ([]byte, error) {
	if key == revision.StorageKey && s.armed.CompareAndSwap(true, false) {
		s.stalls.Add(1)
		if deadline, ok := ctx.Deadline(); ok {
			s.remaining.Store(int64(time.Until(deadline)))
		}
		select {
		case <-time.After(s.stall):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.KVStore.GetBounded(ctx, key, maxBytes)
}

func (s *slowRevisionStore) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	return s.bound.ReadWithLifetime(ctx, key, maxBytes)
}

func (s *slowRevisionStore) ReadBatchWithLifetime(ctx context.Context, keys []string, maxBytes int) ([]storage.LifetimeValue, error) {
	return s.bound.ReadBatchWithLifetime(ctx, keys, maxBytes)
}

func (s *slowRevisionStore) AuthorityTime(ctx context.Context) (time.Time, error) {
	return s.bound.AuthorityTime(ctx)
}

func (s *slowRevisionStore) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	return s.bound.CompareAndSwapInWindow(ctx, mutations, window)
}

// TestAuthorizationColdLoadOutlastsASlowStore holds the gateway's load bound
// against a store that answers one read in two seconds. A cold policy load on
// a loaded CI runner took ten seconds, and the one-second bound that the
// gateway gave the load refused every request with 503 until the machine
// recovered. The bound now sits inside the permission lifetime, because the
// permission's validity starts when the load starts, and above the time a
// slow local store needs.
func TestAuthorizationColdLoadOutlastsASlowStore(t *testing.T) {
	factories := explicitTestFactories(t)
	store := &slowRevisionStore{stall: 2 * time.Second}
	openStorage := factories.openStorage
	factories.openStorage = func(cfg storage.Config) (storage.KVStore, error) {
		backing, err := openStorage(cfg)
		if err != nil {
			return nil, err
		}
		store.KVStore, store.bound = backing, backing.(storage.TimeBoundStore)
		return store, nil
	}
	var dependencies server.Dependencies
	factories.newServer = func(_ *server.Config, value server.Dependencies) (httpRuntime, error) {
		dependencies = value
		return newBlockingHTTPRuntime(), nil
	}
	application, err := New(validProductionConfig(t), withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.NotNil(t, dependencies.Authorization)

	// Construction read the marker already. The cold load is the next read.
	store.armed.Store(true)
	_, err = dependencies.Authorization.Resolve(t.Context(), authorization.Identity{Subject: testAPIKey().Hash})
	require.NoError(t, err, "a cold load slower than one second resolves")
	require.EqualValues(t, 1, store.stalls.Load(), "the stall held the cold load's revision read")
	remaining := time.Duration(store.remaining.Load())
	require.Greater(t, remaining, authorizationLoadTimeout-time.Second, "the load runs under the gateway's load bound")
	require.LessOrEqual(t, remaining, authorizationLoadTimeout)
	require.Less(t, authorizationLoadTimeout, authorizationPermissionLifetime/2, "a load ends well inside the permission lifetime")
}
