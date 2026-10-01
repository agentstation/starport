package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/deployment"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// TestSharedCacheEvictionIsolation proves on real services that cache eviction cannot reach durable records.
// The product refuses a cache service on the durable KV server. On a separate cache service with a memory
// bound and an eviction policy, cache fills evict only cache keys while every durable record stays readable.
func TestSharedCacheEvictionIsolation(t *testing.T) {
	durableURL := os.Getenv("TEST_VALKEY_URL")
	if durableURL == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}
	v := startAdoptValkey(t, "--maxmemory", "4mb", "--maxmemory-policy", "allkeys-lru")
	ctx := t.Context()
	deploymentID := "csp15-eviction-" + strings.ToLower(rand.Text())

	shared := config.CacheConfig{Backend: "valkey", URL: durableURL}
	require.ErrorContains(t, shared.Validate(durableURL), "separate service", "the durable KV server cannot serve the cache")
	separate := config.CacheConfig{Backend: "valkey", URL: v.url}
	require.NoError(t, separate.Validate(durableURL))

	durable, err := storage.OpenValkey(storage.ValkeyConfig{URL: durableURL, DeploymentID: deploymentID})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		keys, err := durable.Scan(cleanup, "*", 0)
		require.NoError(t, err)
		require.NoError(t, durable.BatchDelete(cleanup, keys))
		require.NoError(t, durable.Close())
	})
	records := map[string][]byte{}
	for i := range 256 {
		records[fmt.Sprintf("record:%03d", i)] = bytes.Repeat([]byte("r"), 1<<10)
	}
	require.NoError(t, durable.BatchSet(ctx, records))

	store, err := cache.OpenShared(cache.SharedConfig{URL: v.url, DeploymentID: deploymentID})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.Eventually(t, func() bool { return store.SharedStatus().Available }, 15*time.Second, 50*time.Millisecond)
	prefix, err := deployment.KeyPrefix(deploymentID)
	require.NoError(t, err)
	require.Equal(t, prefix+"cache:", store.SharedStatus().KeyPrefix, "cache keys keep their separate prefix")

	const fills = 96
	value := bytes.Repeat([]byte("c"), 256<<10)
	for i := range fills {
		require.NoError(t, store.Set(ctx, fmt.Sprintf("fill:%03d", i), value, time.Hour))
	}
	require.Positive(t, v.stat(t, v.name, "evicted_keys"), "the memory bound evicts cache entries")
	cached, err := durable.ScanWithPrefix(ctx, "fill:", 0)
	require.NoError(t, err)
	require.Empty(t, cached, "cache fills never reach the durable namespace")
	got, err := durable.BatchGet(ctx, slices.Sorted(maps.Keys(records)))
	require.NoError(t, err)
	require.Equal(t, records, got, "every durable record survives cache eviction")
}
