package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestValkeyIntegration(t *testing.T) {
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}

	store, err := storage.OpenValkey(storage.ValkeyConfig{
		URL:          valkeyURL,
		MaxRetries:   3,
		MinIdleConns: 2,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		DB:           14,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	ctx := context.Background()
	t.Cleanup(func() {
		keys, _ := store.Scan(ctx, "*", 10000)
		if len(keys) > 0 {
			_ = store.BatchDelete(ctx, keys)
		}
	})

	config := ManagerConfig{}
	first, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	second, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })

	t.Run("shared response", func(t *testing.T) {
		want := []byte("shared response")
		require.NoError(t, first.SetResponse(ctx, "shared", want))
		got, found, err := second.GetResponse(ctx, "shared")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, want, got)
	})

	t.Run("local model metadata", func(t *testing.T) {
		want := map[string]any{"id": "test-provider/model"}
		require.NoError(t, first.SetModel(ctx, "test-provider/model", want))
		got, found, err := first.GetModel(ctx, "test-provider/model")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, want, got)

		_, found, err = second.GetModel(ctx, "test-provider/model")
		require.NoError(t, err)
		assert.False(t, found)
	})
}

func TestValkeyPubSubReconnection(t *testing.T) {
	if os.Getenv("TEST_VALKEY_URL") == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}
	t.Skip("UNVERIFIED: this manual test requires a Valkey restart")
}

func BenchmarkValkeyCache(b *testing.B) {
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		b.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}

	store, err := storage.OpenValkey(storage.ValkeyConfig{URL: valkeyURL, MaxRetries: 3, MinIdleConns: 10, DB: 14})
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, store.Close()) })
	manager, err := NewCacheManager(ManagerConfig{}, store)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, manager.Close()) })
	ctx := context.Background()
	require.NoError(b, manager.SetResponse(ctx, "bench", []byte("response")))

	b.Run("GetResponse", func(b *testing.B) {
		for range b.N {
			_, _, _ = manager.GetResponse(ctx, "bench")
		}
	})
}
