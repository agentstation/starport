package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheManagerSingleNodeCachesResponses(t *testing.T) {
	manager, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	ctx := context.Background()
	want := []byte(`{"response":"local"}`)
	require.NoError(t, manager.SetResponse(ctx, "local", want))
	awaitResponse(t, manager, "local", want)
	got, found, err := manager.GetResponse(ctx, "local")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, want, got)
	assert.Contains(t, manager.Stats(), "responses")
}

func TestCacheManagerModelMetadata(t *testing.T) {
	manager, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	ctx := context.Background()
	want := map[string]any{"id": "openai/gpt-4", "context_length": float64(8192)}
	require.NoError(t, manager.SetModel(ctx, "openai/gpt-4", want))
	require.Eventually(t, func() bool { _, found, err := manager.GetModel(ctx, "openai/gpt-4"); return err == nil && found }, time.Second, time.Millisecond)
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

	store, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	manager, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	assert.Same(t, store, manager.responses)
	assert.Equal(t, config.Responses.TTL, manager.config.Responses.TTL)
}

func BenchmarkCacheManager(b *testing.B) {
	manager, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, manager.Close()) })
	ctx := context.Background()
	require.NoError(b, manager.SetResponse(ctx, "bench", []byte(`{"ok":true}`)))
	require.Eventually(b, func() bool { _, found, err := manager.GetResponse(ctx, "bench"); return err == nil && found }, time.Second, time.Millisecond)

	b.Run("GetResponse", func(b *testing.B) {
		for range b.N {
			_, _, _ = manager.GetResponse(ctx, "bench")
		}
	})
}

func TestCacheManagerDefaultIsLocalAndIsolated(t *testing.T) {
	first, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	second, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	require.IsType(t, &LocalCache{}, first.responses)
	require.NoError(t, first.SetResponse(t.Context(), "scoped", []byte("answer")))
	awaitResponse(t, first, "scoped", []byte("answer"))
	value, found, err := first.GetResponse(t.Context(), "scoped")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("answer"), value)
	_, found, err = second.GetResponse(t.Context(), "scoped")
	require.NoError(t, err)
	require.False(t, found)
}

func TestCacheManagerRequiresExplicitSharedStrategy(t *testing.T) {
	for _, strategy := range []string{"", "auto", "local", "invalid"} {
		t.Run(strategy, func(t *testing.T) {
			config := ManagerConfig{}
			config.Responses.Strategy = strategy
			store, err := NewLocalCache(1, time.Minute)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			manager, err := NewCacheManager(config, store)
			require.Error(t, err)
			require.Nil(t, manager)
		})
	}
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, nil)
	require.Error(t, err)
	require.Nil(t, manager)
}

func awaitResponse(t *testing.T, manager *Manager, key string, want []byte) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		got, found, err := manager.GetResponse(t.Context(), key)
		assert.NoError(c, err)
		assert.True(c, found)
		assert.Equal(c, want, got)
	}, time.Second, time.Millisecond)
}
