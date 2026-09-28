package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func incarnationTestStore(t *testing.T, variable string) *ValkeyStore {
	t.Helper()
	address := os.Getenv(variable)
	if address == "" {
		t.Skip("UNVERIFIED: " + variable + " is not set")
	}
	store, err := openUnscopedValkeyForTest(ValkeyConfig{URL: address})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store.(*ValkeyStore)
}

func TestValkeyIncarnationAtomicContract(t *testing.T) {
	store := incarnationTestStore(t, "TEST_VALKEY_URL")
	ctx := t.Context()
	identity, err := store.ObserveIncarnation(ctx)
	require.NoError(t, err)
	bound, err := store.BindIncarnation(ctx, identity)
	require.NoError(t, err)
	prefix := "incarnation:{" + rand.Text() + "}:"
	lease, head := prefix+"lease", prefix+"head"
	t.Cleanup(func() { require.NoError(t, store.BatchDelete(context.Background(), []string{lease, head})) })
	grant := []byte("holder/session/epoch")
	acquire := func(ttl time.Duration) {
		t.Helper()
		require.NoError(t, store.Delete(ctx, lease))
		require.NoError(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{{Key: lease, NewValue: grant, TTL: ttl}}))
	}
	commit := func(expected []byte) error {
		return bound.CompareAndSwap(ctx, []CompareAndSwapMutation{
			{Key: lease, ExpectedValue: grant, NewValue: grant},
			{Key: head, ExpectedValue: expected, NewValue: []byte("accepted")},
		}, lease)
	}
	t.Run("live lease and atomic head", func(t *testing.T) {
		acquire(time.Minute)
		require.NoError(t, commit(nil))
		value, ttl, err := bound.ReadWithLifetime(ctx, head, 64)
		require.NoError(t, err)
		require.Equal(t, "accepted", string(value))
		require.Zero(t, ttl)
		_, ttl, err = bound.ReadWithLifetime(ctx, lease, 64)
		require.NoError(t, err)
		require.Positive(t, ttl)
		require.LessOrEqual(t, ttl, time.Minute)
		require.ErrorIs(t, commit(nil), ErrConflict)
	})
	t.Run("expired native lease", func(t *testing.T) {
		acquire(30 * time.Millisecond)
		require.Eventually(t, func() bool {
			_, _, err := bound.ReadWithLifetime(ctx, lease, 64)
			return err == ErrNotFound
		}, 3*time.Second, 5*time.Millisecond)
		require.ErrorIs(t, commit([]byte("accepted")), ErrConflict)
	})
	t.Run("released native lease", func(t *testing.T) {
		acquire(time.Minute)
		require.NoError(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{{Key: lease, ExpectedValue: grant}}, lease))
		require.ErrorIs(t, commit([]byte("accepted")), ErrConflict)
	})
	t.Run("matching value without native expiry", func(t *testing.T) {
		require.NoError(t, store.Set(ctx, lease, grant))
		require.ErrorIs(t, commit([]byte("accepted")), ErrConflict)
	})
	t.Run("mismatch changes no key", func(t *testing.T) {
		acquire(time.Minute)
		require.ErrorIs(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{
			{Key: lease, ExpectedValue: grant, NewValue: []byte("replacement"), TTL: time.Minute},
			{Key: head, ExpectedValue: []byte("wrong"), NewValue: []byte("wrong")},
		}, lease), ErrConflict)
		value, _, err := bound.ReadWithLifetime(ctx, lease, 64)
		require.NoError(t, err)
		require.Equal(t, grant, value)
	})
	t.Run("bounded reads and invalid mutations", func(t *testing.T) {
		_, _, err := bound.ReadWithLifetime(ctx, head, 1)
		require.ErrorIs(t, err, ErrValueTooLarge)
		require.ErrorIs(t, bound.CompareAndSwap(ctx, nil, lease), ErrInvalidMutation)
		require.ErrorIs(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{{Key: lease}}, lease), ErrInvalidMutation)
		require.ErrorIs(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{{Key: head}, {Key: head}}), ErrInvalidMutation)
	})
	t.Run("one concurrent winner", func(t *testing.T) {
		acquire(time.Minute)
		require.NoError(t, store.Delete(ctx, head))
		var wins atomic.Int32
		unexpected := make(chan error, 16)
		var group sync.WaitGroup
		for range 16 {
			group.Go(func() {
				err := commit(nil)
				if err == nil {
					wins.Add(1)
				} else if !errors.Is(err, ErrConflict) {
					unexpected <- err
				}
			})
		}
		group.Wait()
		close(unexpected)
		for err := range unexpected {
			require.NoError(t, err)
		}
		require.Equal(t, int32(1), wins.Load())
	})
	t.Run("unapproved identity", func(t *testing.T) {
		_, err := store.BindIncarnation(ctx, "unknown")
		require.ErrorIs(t, err, ErrIncarnationChanged)
	})
}

func TestValkeyIncarnationRejectsReplacementBackend(t *testing.T) {
	first := incarnationTestStore(t, "TEST_VALKEY_URL")
	second := incarnationTestStore(t, "TEST_VALKEY_REPLACEMENT_URL")
	ctx := t.Context()
	identity, err := first.ObserveIncarnation(ctx)
	require.NoError(t, err)
	replacement, err := second.ObserveIncarnation(ctx)
	require.NoError(t, err)
	require.NotEqual(t, identity, replacement, "test requires two independent backend processes")
	_, err = second.BindIncarnation(ctx, identity)
	require.ErrorIs(t, err, ErrIncarnationChanged)
	// Use the original approval on the replacement connection. Its records cannot approve it.
	bound := &valkeyIncarnationStore{store: second, identity: identity}
	key := "incarnation:{" + rand.Text() + "}:restored"
	require.NoError(t, second.Set(ctx, key, []byte("restored")))
	t.Cleanup(func() { require.NoError(t, second.Delete(context.Background(), key)) })
	value, _, err := bound.ReadWithLifetime(ctx, key, 64)
	require.ErrorIs(t, err, ErrIncarnationChanged)
	require.Nil(t, value)
	values, err := bound.ReadBatchWithLifetime(ctx, []string{key}, 64)
	require.ErrorIs(t, err, ErrIncarnationChanged)
	require.Nil(t, values)
	require.ErrorIs(t, bound.CompareAndSwap(ctx, []CompareAndSwapMutation{
		{Key: key, ExpectedValue: []byte("restored"), NewValue: []byte("unapproved")},
	}), ErrIncarnationChanged)
	value, err = second.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "restored", string(value))
}
