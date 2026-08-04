package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestCacheManagerMultiNodeSharesResponses(t *testing.T) {
	sharedStore := storage.NewMockStore()
	sharedPubSub := NewMemoryPubSub()
	store := &mockStoreWithPubSub{KVStore: sharedStore, pubsub: sharedPubSub}
	config := ManagerConfig{}

	first, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	second, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })

	ctx := context.Background()
	want := []byte(`{"response":"shared"}`)
	require.NoError(t, first.SetResponse(ctx, "shared", want))
	got, found, err := second.GetResponse(ctx, "shared")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, want, got)
}

func TestCacheManagerSingleNodeCachesResponses(t *testing.T) {
	manager, err := NewCacheManager(ManagerConfig{}, storage.NewMockStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	ctx := context.Background()
	want := []byte(`{"response":"local"}`)
	require.NoError(t, manager.SetResponse(ctx, "local", want))
	got, found, err := manager.GetResponse(ctx, "local")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, want, got)
	assert.Contains(t, manager.Stats(), "responses")
}

func TestCacheManagerModelMetadata(t *testing.T) {
	manager, err := NewCacheManager(ManagerConfig{}, storage.NewMockStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	ctx := context.Background()
	want := map[string]any{"id": "openai/gpt-4", "context_length": float64(8192)}
	require.NoError(t, manager.SetModel(ctx, "openai/gpt-4", want))
	got, found, err := manager.GetModel(ctx, "openai/gpt-4")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, want, got)

	manager.InvalidateModels()
	_, found, err = manager.GetModel(ctx, "openai/gpt-4")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestCacheManagerConfig(t *testing.T) {
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	config.Responses.TTL = time.Minute
	config.Responses.MaxItemSizeKB = 16
	config.Models.TTL = time.Hour
	config.Models.SizeMB = 8

	manager, err := NewCacheManager(config, storage.NewMockStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	assert.IsType(t, &DistributedCache{}, manager.responses)
	assert.Equal(t, config.Responses.TTL, manager.config.Responses.TTL)
}

type mockStoreWithPubSub struct {
	storage.KVStore
	pubsub PubSubClient
}

func (m *mockStoreWithPubSub) GetPubSub() PubSubClient {
	return m.pubsub
}

func BenchmarkCacheManager(b *testing.B) {
	manager, err := NewCacheManager(ManagerConfig{}, storage.NewMockStore())
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, manager.Close()) })
	ctx := context.Background()
	require.NoError(b, manager.SetResponse(ctx, "bench", []byte(`{"ok":true}`)))

	b.Run("GetResponse", func(b *testing.B) {
		for range b.N {
			_, _, _ = manager.GetResponse(ctx, "bench")
		}
	})
}
