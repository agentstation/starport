package storage

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/deployment"
	"github.com/stretchr/testify/require"
)

// TestValkeyDeploymentHashSlotLayout proves the one-slot layout of one deployment on the real service.
// Every durable key, control key, and notification channel carries the deployment hash tag first, so one
// Cluster slot would own them all, including the keys of multi-key conditional mutations.
// The layout is a guard for a future qualification. It does not qualify Cluster mode, which stays refused.
func TestValkeyDeploymentHashSlotLayout(t *testing.T) {
	store := deploymentTestStore(t, "slot:"+rand.Text()+":{東京}", "/0")
	ctx := t.Context()
	prefix, err := deployment.KeyPrefix(store.config.DeploymentID)
	require.NoError(t, err)
	tag := "{" + prefix + "}"
	require.Equal(t, tag+"kv:", store.prefix)
	require.Equal(t, store.prefix, store.pubsub.prefix, "notification channels share the durable prefix")
	slot := func(key string) uint16 {
		command := store.client.B().Get().Key(key).Build()
		return command.Slot()
	}
	expected := slot(tag)

	logical := []string{
		"account:v1:a", "identity:v1:b", "ratelimit:v1:subject:c", "limits:v2:job_claims:d", "jobs:v1:account:e",
		"files:v1:account:f", "batches:v1:account:g", "usage:v1:h", "credentials:v1:i", "presets:v1:j", "authmode:v1",
		"catalog_generation:v1:k", "catalog:archive:v1:l", "budget:v1:attempt:m", "{caller-selected-tag}:lease",
		TransferBarrierKey, transferActivationCurrent, transferActivationPrefix + "digest", transferReconciliationCurrent,
		transferReconciliationPrefix + "plan", transferPopulatedPrefix + "n", "events:change",
	}
	for _, key := range logical {
		require.Equal(t, expected, slot(store.prefix+key), key)
	}

	t.Run("multi-key conditional mutations stay in the slot", func(t *testing.T) {
		identity, err := store.ObserveIncarnation(ctx)
		require.NoError(t, err)
		bound, err := store.BindIncarnation(ctx, identity)
		require.NoError(t, err)
		require.NoError(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{{Key: "lease", NewValue: []byte("own"), TTL: time.Minute}}))
		require.NoError(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{
			{Key: "lease", ExpectedValue: []byte("own"), NewValue: []byte("own")},
			{Key: "head", NewValue: []byte("accepted")},
			{Key: TransferBarrierKey, NewValue: []byte("barrier")},
		}, "lease"))
		require.NoError(t, store.CompareAndSwapBatch(ctx, []CompareAndSwapMutation{
			{Key: "head", ExpectedValue: []byte("accepted"), NewValue: []byte("next")},
			{Key: transferActivationCurrent, NewValue: []byte("current")},
		}))
		var cursor uint64
		var physical []string
		for {
			result, err := store.client.Do(ctx, store.client.B().Scan().Cursor(cursor).Match(tag+"*").Count(1000).Build()).AsScanEntry()
			require.NoError(t, err)
			physical = append(physical, result.Elements...)
			cursor = result.Cursor
			if cursor == 0 {
				break
			}
		}
		require.Len(t, physical, 4, "the real service holds exactly the written keys under the deployment tag")
		for _, key := range physical {
			require.True(t, strings.HasPrefix(key, store.prefix), key)
			require.Equal(t, expected, slot(key), key)
		}
	})

	t.Run("cache keys stay outside the durable slot", func(t *testing.T) {
		cachePrefix := prefix + "cache:"
		require.NotContains(t, cachePrefix, "{", "cache keys carry no hash tag and live on a separate service")
		require.NotEqual(t, expected, slot(cachePrefix+"response:x"), "a cache key does not share the deployment slot by construction")
	})

	t.Run("cluster mode stays refused", func(t *testing.T) {
		selected := store.config
		selected.ClusterMode = true
		require.ErrorContains(t, selected.ValidateConnection(), "cluster mode is not qualified")
	})
}
