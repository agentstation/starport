package app

import (
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// TestValkeyDurabilityAndFailover qualifies the durability contract of the qualified Valkey release on a private
// persistent process. A persistent restart keeps every acknowledged write. A promoted replica keeps every
// replicated write. A write that only the old primary acknowledged after promotion is lost, and the demoted
// owner refuses bound product writes. External fencing of an unreachable old primary remains mandatory.
// The first replica attachment after a primary start also changes the identity, which the test records as a limit.
func TestValkeyDurabilityAndFailover(t *testing.T) {
	v := startAdoptValkey(t)
	ctx := t.Context()
	deploymentID := "csp15-failover-" + strings.ToLower(rand.Text())
	open := func(address string) (storage.KVStore, storage.IncarnationStore, string) {
		t.Helper()
		store, err := storage.OpenValkey(storage.ValkeyConfig{URL: address, DeploymentID: deploymentID})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		provider := store.(storage.IncarnationProvider)
		identity, err := provider.ObserveIncarnation(ctx)
		require.NoError(t, err)
		bound, err := provider.BindIncarnation(ctx, identity)
		require.NoError(t, err)
		return store, bound, identity
	}
	refused := func(bound storage.IncarnationStore) {
		t.Helper()
		require.Eventually(t, func() bool {
			err := bound.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{{Key: "stale", NewValue: []byte("stale")}})
			return errors.Is(err, storage.ErrIncarnationChanged)
		}, 15*time.Second, 100*time.Millisecond, "a stale owner must observe the incarnation change")
	}
	head := func(bound storage.IncarnationStore) string {
		t.Helper()
		value, _, err := bound.ReadWithLifetime(ctx, "head", 64)
		require.NoError(t, err)
		return string(value)
	}
	records := map[string][]byte{}
	for i := range 64 {
		records[fmt.Sprintf("record:%02d", i)] = []byte(strings.Repeat("d", 512))
	}
	keys := slices.Sorted(maps.Keys(records))
	retained := func(store storage.KVStore) {
		t.Helper()
		got, err := store.BatchGet(ctx, keys)
		require.NoError(t, err)
		require.Equal(t, records, got)
	}

	first, firstBound, firstIdentity := open(v.url)
	require.NoError(t, first.BatchSet(ctx, records))
	require.NoError(t, firstBound.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{{Key: "head", NewValue: []byte("before-restart")}}))

	t.Log("persistent restart")
	v.restart(t)
	refused(firstBound)
	second, secondBound, secondIdentity := open(v.url)
	require.NotEqual(t, firstIdentity, secondIdentity, "a restart changes the process identity")
	retained(second)
	require.Equal(t, "before-restart", head(secondBound))

	t.Log("first replica attachment")
	replica, replicaAddress := v.replica(t)
	// Documented limit: the first replica attachment to a primary without a replication backlog assigns a new
	// replication history identity. The bound owner fails closed and needs re-admission before it writes again.
	refused(secondBound)
	third, thirdBound, thirdIdentity := open(v.url)
	require.NotEqual(t, secondIdentity, thirdIdentity, "the first replica attachment changes the replication history")
	retained(third)
	require.NoError(t, thirdBound.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{
		{Key: "head", ExpectedValue: []byte("before-restart"), NewValue: []byte("before-promotion")},
	}))

	t.Log("replica promotion")
	v.promote(t, replica)
	// Without demotion the old primary still acknowledges a write that no replica receives.
	require.NoError(t, third.Set(ctx, "late", []byte("acknowledged by the old primary only")))
	v.demote(t, v.name, replica)
	refused(thirdBound)
	require.Eventually(t, func() bool { return third.Set(ctx, "late", []byte("again")) != nil }, 15*time.Second, 100*time.Millisecond, "a demoted owner refuses plain writes")

	fourth, fourthBound, fourthIdentity := open(replicaAddress)
	require.NotEqual(t, thirdIdentity, fourthIdentity, "promotion opens a new replication history")
	retained(fourth)
	require.Equal(t, "before-promotion", head(fourthBound))
	_, err := fourth.Get(ctx, "late")
	require.ErrorIs(t, err, storage.ErrNotFound, "promotion loses a write that only the old primary acknowledged")
	require.Eventually(t, func() bool {
		_, err := third.Get(ctx, "late")
		return errors.Is(err, storage.ErrNotFound)
	}, 15*time.Second, 100*time.Millisecond, "the demoted owner discards its unreplicated write")
}
