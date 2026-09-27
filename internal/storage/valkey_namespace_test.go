package storage

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func deploymentTestStore(t *testing.T, id, database string) *ValkeyStore {
	t.Helper()
	endpoint := os.Getenv("TEST_VALKEY_URL")
	if endpoint == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is required")
	}
	u, err := url.Parse(endpoint)
	require.NoError(t, err)
	u.Path = database
	kv, err := OpenValkey(ValkeyConfig{URL: u.String(), DeploymentID: id})
	require.NoError(t, err)
	v := kv.(*ValkeyStore)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		keys, err := v.Scan(ctx, "*", 0)
		require.NoError(t, err)
		require.NoError(t, v.BatchDelete(ctx, keys))
		require.NoError(t, v.Close())
	})
	return v
}

func TestValkeyRequiresDeploymentIdentity(t *testing.T) {
	for _, id := range []string{"", " leading", "control\n"} {
		kv, err := OpenValkey(ValkeyConfig{URL: "redis://127.0.0.1:1", DeploymentID: id})
		require.ErrorContains(t, err, "deployment identity")
		require.Nil(t, kv)
	}
}

func TestValkeyDeploymentNamespace(t *testing.T) {
	a := deploymentTestStore(t, rand.Text()+":{*?[]}", "/0")
	b := deploymentTestStore(t, rand.Text()+":{*?[]}", "/0")
	ctx := t.Context()
	key := "same:{caller-selected-tag}"
	require.NoError(t, a.Set(ctx, key, []byte("first")))
	_, err := b.Get(ctx, key)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, b.Set(ctx, key, []byte("second")))
	value, err := a.GetBounded(ctx, key, 5)
	require.NoError(t, err)
	require.Equal(t, "first", string(value))
	_, err = a.GetBounded(ctx, key, 4)
	require.ErrorIs(t, err, ErrValueTooLarge)
	require.NoError(t, b.Delete(ctx, key))
	exists, err := a.Exists(ctx, key)
	require.NoError(t, err)
	require.True(t, exists)

	t.Run("ttl and bounded lifetime", func(t *testing.T) {
		require.NoError(t, a.SetWithTTL(ctx, "ttl", []byte("a"), time.Minute))
		require.NoError(t, b.SetWithTTL(ctx, "ttl", []byte("b"), 2*time.Minute))
		require.NoError(t, a.ExpireAt(ctx, "ttl", time.Now().Add(-time.Second)))
		_, err := a.GetTTL(ctx, "ttl")
		require.ErrorIs(t, err, ErrNotFound)
		ttl, err := b.GetTTL(ctx, "ttl")
		require.NoError(t, err)
		require.Greater(t, ttl, time.Minute)
		value, remaining, err := b.ReadWithLifetime(ctx, "ttl", 10)
		require.NoError(t, err)
		require.Equal(t, "b", string(value))
		require.Greater(t, remaining, time.Minute)
	})
	t.Run("counters", func(t *testing.T) {
		value, err := a.Increment(ctx, "count", 7)
		require.NoError(t, err)
		require.EqualValues(t, 7, value)
		value, err = b.Decrement(ctx, "count", 2)
		require.NoError(t, err)
		require.EqualValues(t, -2, value)
	})
	t.Run("batches and scans", func(t *testing.T) {
		items := map[string][]byte{"batch:one": []byte("one"), "batch:two": []byte("two")}
		require.NoError(t, a.BatchSet(ctx, items))
		require.NoError(t, b.BatchSetWithTTL(ctx, items, time.Minute))
		require.NoError(t, a.BatchDelete(ctx, []string{"batch:one", "batch:two"}))
		got, err := b.BatchGet(ctx, []string{"batch:one", "batch:two", "absent"})
		require.NoError(t, err)
		require.Equal(t, items, got)
		keys, err := a.Scan(ctx, "batch:*", 0)
		require.NoError(t, err)
		require.Empty(t, keys)
		keys, err = b.ScanWithPrefix(ctx, "batch:", 0)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"batch:one", "batch:two"}, keys)
		keys, err = b.Scan(ctx, "batch:*", 1)
		require.NoError(t, err)
		require.Len(t, keys, 1)
	})
	t.Run("atomic conditional writes", func(t *testing.T) {
		require.NoError(t, a.CompareAndSwap(ctx, "cas", nil, []byte("a")))
		require.NoError(t, b.CompareAndSwap(ctx, "cas", nil, []byte("b")))
		require.ErrorIs(t, a.CompareAndSwapBatch(ctx, []CompareAndSwapMutation{
			{Key: "cas", ExpectedValue: []byte("b"), NewValue: []byte("wrong")},
			{Key: "must-not-exist", NewValue: []byte("wrong")},
		}), ErrConflict)
		_, err := a.Get(ctx, "must-not-exist")
		require.ErrorIs(t, err, ErrNotFound)
		value, err := b.Get(ctx, "cas")
		require.NoError(t, err)
		require.Equal(t, "b", string(value))
	})
	t.Run("native ownership checks", func(t *testing.T) {
		id, err := a.ObserveIncarnation(ctx)
		require.NoError(t, err)
		bound, err := a.BindIncarnation(ctx, id)
		require.NoError(t, err)
		require.NoError(t, b.Set(ctx, "lease", []byte("foreign")))
		require.NoError(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{{Key: "lease", NewValue: []byte("own"), TTL: time.Minute}}))
		require.NoError(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{
			{Key: "lease", ExpectedValue: []byte("own"), NewValue: []byte("own")},
			{Key: "head", NewValue: []byte("accepted")},
		}, "lease"))
		value, ttl, err := bound.ReadWithLifetime(ctx, "lease", 10)
		require.NoError(t, err)
		require.Equal(t, "own", string(value))
		require.Positive(t, ttl)
		value, err = b.Get(ctx, "lease")
		require.NoError(t, err)
		require.Equal(t, "foreign", string(value))
		_, err = b.Get(ctx, "head")
		require.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("atomic slot layout", func(t *testing.T) {
		var slot uint16
		for i, logical := range []string{"account:a", "key:b", "{foreign-tag}:lease", "job:c"} {
			cmd := a.client.B().Get().Key(a.prefix + logical).Build()
			if i == 0 {
				slot = cmd.Slot()
			}
			require.Equal(t, slot, cmd.Slot())
		}
	})
}

func TestValkeyDeploymentNotificationIsolation(t *testing.T) {
	a := deploymentTestStore(t, rand.Text(), "/0")
	b := deploymentTestStore(t, rand.Text(), "/1")
	left, right := make(chan string, 32), make(chan string, 32)
	subscribe := func(v *ValkeyStore, output chan string) {
		t.Helper()
		require.NoError(t, v.GetPubSub().Subscribe("events:*", func(channel, value string) { output <- channel + ":" + value }))
		require.Eventually(t, func() bool {
			require.NoError(t, v.GetPubSub().Publish(t.Context(), "events:ready", "ready"))
			select {
			case <-output:
				return true
			default:
				return false
			}
		}, 3*time.Second, 10*time.Millisecond)
	}
	subscribe(a, left)
	subscribe(b, right)
	require.NoError(t, a.GetPubSub().Publish(t.Context(), "events:change", "foreign"))
	require.NoError(t, b.GetPubSub().Publish(t.Context(), "events:change", "own"))
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case got := <-right:
			if got == "events:ready:ready" {
				continue
			}
			require.Equal(t, "events:change:own", got)
			return
		case <-timer.C:
			t.Fatal("deployment notification did not arrive")
		}
	}
}
