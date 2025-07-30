package storage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go/mock"
	"go.uber.org/mock/gomock"
)

// TestValkeyStoreWithMock tests ValkeyStore using the valkey mock
func TestValkeyStoreWithMock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create a mock valkey client
	client := mock.NewClient(ctrl)

	// Create ValkeyStore with mock client
	store := &ValkeyStore{
		client: client,
		config: ValkeyConfig{
			URL: "mock://localhost",
		},
		pubsub: NewValkeyPubSub(client),
	}

	ctx := context.Background()

	t.Run("Basic Operations", func(t *testing.T) {
		key := "test:key"
		value := []byte("test value")

		// Set
		client.EXPECT().Do(ctx, mock.Match("SET", key, string(value))).Return(mock.Result(mock.ValkeyString("OK")))
		err := store.Set(ctx, key, value)
		assert.NoError(t, err)

		// Get
		client.EXPECT().Do(ctx, mock.Match("GET", key)).Return(mock.Result(mock.ValkeyString(string(value))))
		got, err := store.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, value, got)

		// Get missing key
		client.EXPECT().Do(ctx, mock.Match("GET", "missing")).Return(mock.Result(mock.ValkeyNil()))
		_, err = store.Get(ctx, "missing")
		assert.Equal(t, ErrNotFound, err)

		// Exists
		client.EXPECT().Do(ctx, mock.Match("EXISTS", key)).Return(mock.Result(mock.ValkeyInt64(1)))
		exists, err := store.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)

		// Delete
		client.EXPECT().Do(ctx, mock.Match("DEL", key)).Return(mock.Result(mock.ValkeyInt64(1)))
		err = store.Delete(ctx, key)
		assert.NoError(t, err)
	})

	t.Run("TTL Operations", func(t *testing.T) {
		key := "test:ttl:key"
		value := []byte("ttl value")
		ttl := 10 * time.Second

		// SetWithTTL
		client.EXPECT().Do(ctx, mock.Match("SET", key, string(value), "EX", "10")).Return(mock.Result(mock.ValkeyString("OK")))
		err := store.SetWithTTL(ctx, key, value, ttl)
		assert.NoError(t, err)

		// GetTTL
		client.EXPECT().Do(ctx, mock.Match("TTL", key)).Return(mock.Result(mock.ValkeyInt64(10)))
		gotTTL, err := store.GetTTL(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, 10*time.Second, gotTTL)

		// GetTTL for non-existent key
		client.EXPECT().Do(ctx, mock.Match("TTL", "missing")).Return(mock.Result(mock.ValkeyInt64(-2)))
		_, err = store.GetTTL(ctx, "missing")
		assert.Equal(t, ErrNotFound, err)

		// ExpireAt
		expireAt := time.Now().Add(1 * time.Hour)
		client.EXPECT().Do(ctx, mock.MatchFn(func(cmd []string) bool {
			return len(cmd) == 3 && cmd[0] == "EXPIREAT" && cmd[1] == key
		})).Return(mock.Result(mock.ValkeyInt64(1)))
		err = store.ExpireAt(ctx, key, expireAt)
		assert.NoError(t, err)
	})

	t.Run("Atomic Operations", func(t *testing.T) {
		key := "test:counter"

		// Increment
		client.EXPECT().Do(ctx, mock.Match("INCRBY", key, "5")).Return(mock.Result(mock.ValkeyInt64(5)))
		val, err := store.Increment(ctx, key, 5)
		assert.NoError(t, err)
		assert.Equal(t, int64(5), val)

		// Decrement
		client.EXPECT().Do(ctx, mock.Match("DECRBY", key, "2")).Return(mock.Result(mock.ValkeyInt64(3)))
		val, err = store.Decrement(ctx, key, 2)
		assert.NoError(t, err)
		assert.Equal(t, int64(3), val)
	})

	t.Run("Batch Operations", func(t *testing.T) {
		keys := []string{"key1", "key2", "key3"}

		// BatchGet
		client.EXPECT().Do(ctx, mock.Match("MGET", "key1", "key2", "key3")).Return(
			mock.Result(mock.ValkeyArray(
				mock.ValkeyString("value1"),
				mock.ValkeyString("value2"),
				mock.ValkeyNil(),
			)),
		)
		result, err := store.BatchGet(ctx, keys)
		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, []byte("value1"), result["key1"])
		assert.Equal(t, []byte("value2"), result["key2"])

		// BatchDelete
		client.EXPECT().Do(ctx, mock.Match("DEL", "key1", "key2", "key3")).Return(mock.Result(mock.ValkeyInt64(3)))
		err = store.BatchDelete(ctx, keys)
		assert.NoError(t, err)
	})

	t.Run("Scan Operations", func(t *testing.T) {
		pattern := "test:*"

		// Scan - first call
		client.EXPECT().Do(ctx, mock.Match("SCAN", "0", "MATCH", pattern, "COUNT", "100")).Return(
			mock.Result(mock.ValkeyArray(
				mock.ValkeyString("5"), // next cursor
				mock.ValkeyArray(
					mock.ValkeyString("test:1"),
					mock.ValkeyString("test:2"),
					mock.ValkeyString("test:3"),
				),
			)),
		)
		// Scan - second call
		client.EXPECT().Do(ctx, mock.Match("SCAN", "5", "MATCH", pattern, "COUNT", "100")).Return(
			mock.Result(mock.ValkeyArray(
				mock.ValkeyString("0"), // cursor 0 means done
				mock.ValkeyArray(
					mock.ValkeyString("test:4"),
					mock.ValkeyString("test:5"),
				),
			)),
		)

		keys, err := store.Scan(ctx, pattern, 100)
		assert.NoError(t, err)
		assert.Equal(t, []string{"test:1", "test:2", "test:3", "test:4", "test:5"}, keys)

		// ScanWithPrefix
		prefix := "prefix:"
		client.EXPECT().Do(ctx, mock.Match("SCAN", "0", "MATCH", prefix+"*", "COUNT", "10")).Return(
			mock.Result(mock.ValkeyArray(
				mock.ValkeyString("0"),
				mock.ValkeyArray(
					mock.ValkeyString("prefix:a"),
					mock.ValkeyString("prefix:b"),
				),
			)),
		)
		keys, err = store.ScanWithPrefix(ctx, prefix, 10)
		assert.NoError(t, err)
		assert.Equal(t, []string{"prefix:a", "prefix:b"}, keys)
	})

	t.Run("Health Check", func(t *testing.T) {
		// Ping
		client.EXPECT().Do(ctx, mock.Match("PING")).Return(mock.Result(mock.ValkeyString("PONG")))
		err := store.Ping(ctx)
		assert.NoError(t, err)
	})

	t.Run("CompareAndSwap", func(t *testing.T) {
		key := "test:cas"
		oldValue := []byte("old")
		newValue := []byte("new")

		// CAS success
		client.EXPECT().Do(ctx, mock.MatchFn(func(cmd []string) bool {
			return len(cmd) >= 2 && cmd[0] == "EVAL"
		})).Return(mock.Result(mock.ValkeyString("OK")))
		err := store.CompareAndSwap(ctx, key, oldValue, newValue)
		assert.NoError(t, err)

		// For CAS failure test, we'll skip it as the mock behavior
		// with ValkeyNil is complex to simulate correctly
		// The integration tests cover this scenario
	})
}

// TestBatchSetWithMock tests the BatchSet operation that doesn't use MSET
func TestBatchSetWithMock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mock.NewClient(ctrl)
	store := &ValkeyStore{
		client: client,
		config: ValkeyConfig{
			URL: "mock://localhost",
		},
	}

	ctx := context.Background()

	items := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
		"key3": []byte("value3"),
	}

	// Expect individual SET commands for each item
	// Note: order may vary due to map iteration
	client.EXPECT().Do(ctx, gomock.Any()).Return(mock.Result(mock.ValkeyString("OK"))).Times(3)

	err := store.BatchSet(ctx, items)
	assert.NoError(t, err)
}

// TestBatchSetWithTTLMock tests BatchSetWithTTL using mock
func TestBatchSetWithTTLMock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mock.NewClient(ctrl)
	store := &ValkeyStore{
		client: client,
		config: ValkeyConfig{
			URL: "mock://localhost",
		},
	}

	ctx := context.Background()
	ttl := 1 * time.Hour

	items := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}

	// Expect individual SET commands with TTL for each item
	// Note: order may vary due to map iteration
	client.EXPECT().Do(ctx, gomock.Any()).Return(mock.Result(mock.ValkeyString("OK"))).Times(2)

	err := store.BatchSetWithTTL(ctx, items, ttl)
	assert.NoError(t, err)
}

// TestValkeyTransactionWithMock tests ValkeyTransaction using mock
func TestValkeyTransactionWithMock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mock.NewClient(ctrl)
	store := &ValkeyStore{
		client: client,
		config: ValkeyConfig{
			URL: "mock://localhost",
		},
	}

	ctx := context.Background()

	t.Run("Transaction Structure", func(t *testing.T) {
		// Begin transaction
		tx, err := store.BeginTransaction(ctx)
		require.NoError(t, err)

		// Queue operations
		err = tx.Set("key1", []byte("value1"))
		assert.NoError(t, err)

		err = tx.SetWithTTL("key2", []byte("value2"), 10*time.Second)
		assert.NoError(t, err)

		err = tx.Delete("key3")
		assert.NoError(t, err)

		// Verify transaction has commands queued
		vtx := tx.(*ValkeyTransaction)
		assert.Len(t, vtx.commands, 3)
	})

	t.Run("Transaction Rollback", func(t *testing.T) {
		tx, err := store.BeginTransaction(ctx)
		require.NoError(t, err)

		// Queue some operations
		_ = tx.Set("key", []byte("value"))

		// Rollback
		err = tx.Rollback()
		assert.NoError(t, err)

		// Verify state is cleared
		vtx := tx.(*ValkeyTransaction)
		assert.Nil(t, vtx.commands)
		assert.Nil(t, vtx.state)
	})
}

// TestPubSubWithMock tests pub/sub operations with mock
func TestPubSubWithMock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mock.NewClient(ctrl)
	pubsub := NewValkeyPubSub(client)

	ctx := context.Background()

	t.Run("Publish", func(t *testing.T) {
		channel := "test:channel"
		message := "test message"

		client.EXPECT().Do(ctx, mock.Match("PUBLISH", channel, message)).Return(mock.Result(mock.ValkeyInt64(1)))

		err := pubsub.Publish(ctx, channel, message)
		assert.NoError(t, err)
	})
}
