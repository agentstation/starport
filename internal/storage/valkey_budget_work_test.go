package storage

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

type budgetCommandClient struct {
	valkey.Client
	commands []string
}

func (c *budgetCommandClient) Do(ctx context.Context, command valkey.Completed) valkey.ValkeyResult {
	c.commands = append(c.commands, command.Commands()[0])
	return c.Client.Do(ctx, command)
}

func (c *budgetCommandClient) DoMulti(ctx context.Context, commands ...valkey.Completed) []valkey.ValkeyResult {
	for _, command := range commands {
		c.commands = append(c.commands, command.Commands()[0])
	}
	return c.Client.DoMulti(ctx, commands...)
}

func TestBudgetAuthorityNativeOperationCommands(t *testing.T) {
	store := incarnationTestStore(t, "TEST_VALKEY_URL")
	id, err := store.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	bound, err := store.BindIncarnation(t.Context(), id)
	require.NoError(t, err)
	authority := bound.(TimeBoundStore)
	counter := &budgetCommandClient{Client: store.client}
	store.client = counter
	check := func(operation func()) {
		t.Helper()
		counter.commands = nil
		operation()
		require.Equal(t, []string{"EVAL"}, counter.commands, "one authority operation must issue one native command")
	}
	key := "budget:v1:probe:" + rand.Text()
	var now time.Time
	check(func() {
		now, err = authority.AuthorityTime(t.Context())
		require.NoError(t, err)
	})
	check(func() {
		_, _, err := authority.ReadWithLifetime(t.Context(), key, 64)
		require.ErrorIs(t, err, ErrNotFound)
	})
	t.Cleanup(func() { require.NoError(t, store.Delete(context.Background(), key)) })
	window := TimeWindow{Start: now.Truncate(time.Second).Add(-time.Minute), End: now.Truncate(time.Second).Add(time.Hour)}
	check(func() {
		require.NoError(t, authority.CompareAndSwapInWindow(t.Context(), []CompareAndSwapMutation{{Key: key, NewValue: []byte("reserved")}}, window))
	})
	check(func() {
		data, ttl, err := authority.ReadWithLifetime(t.Context(), key, 64)
		require.NoError(t, err)
		require.Equal(t, "reserved", string(data))
		require.Zero(t, ttl)
	})
	check(func() {
		values, err := authority.ReadBatchWithLifetime(t.Context(), []string{key, key + ":missing"}, 64)
		require.NoError(t, err)
		require.Equal(t, []LifetimeValue{{Value: []byte("reserved"), Found: true}, {}}, values)
	})
	check(func() {
		require.NoError(t, authority.CompareAndSwapInWindow(t.Context(), []CompareAndSwapMutation{{Key: key, ExpectedValue: []byte("reserved"), NewValue: []byte("settled")}}, window))
	})
}
