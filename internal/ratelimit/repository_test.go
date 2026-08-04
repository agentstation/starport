package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repositorytest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRateLimitRepositoryContract(t *testing.T) {
	repositorytest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		clock := &fakeClock{now: time.Unix(100, 0).UTC()}
		repository, err := Open(store, clock)
		require.NoError(t, err)
		subject := "api-key:" + uuid.NewString()

		first, err := repository.Consume(ctx, subject, 2, time.Minute)
		require.NoError(t, err)
		require.True(t, first.Allowed)
		require.EqualValues(t, 1, first.Count)
		require.EqualValues(t, 1, first.Remaining)
		ttl, err := store.GetTTL(ctx, storageKey(subject))
		require.NoError(t, err)
		require.Greater(t, ttl, time.Duration(0))
		second, err := repository.Consume(ctx, subject, 2, time.Minute)
		require.NoError(t, err)
		require.True(t, second.Allowed)
		third, err := repository.Consume(ctx, subject, 2, time.Minute)
		require.NoError(t, err)
		require.False(t, third.Allowed)
		require.Zero(t, third.Remaining)

		data, err := store.Get(ctx, storageKey(subject))
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(data, &schema))
		require.EqualValues(t, StorageSchemaVersion, schema["schema_version"])

		clock.Advance(time.Minute)
		reset, err := repository.Consume(ctx, subject, 2, time.Minute)
		require.NoError(t, err)
		require.True(t, reset.Allowed)
		require.EqualValues(t, 1, reset.Count)
		require.Equal(t, clock.Now().Add(time.Minute), reset.ResetAt)
		ttl, err = store.GetTTL(ctx, storageKey(subject))
		require.NoError(t, err)
		require.Greater(t, ttl, time.Duration(0))

		_, err = repository.Consume(ctx, "", 1, time.Minute)
		require.ErrorIs(t, err, ErrInvalidSubject)
		_, err = repository.Consume(ctx, subject, 0, time.Minute)
		require.ErrorIs(t, err, ErrInvalidLimit)
		_, err = repository.Consume(ctx, subject, 1, 0)
		require.ErrorIs(t, err, ErrInvalidWindow)

		corruptSubject := "corrupt:" + uuid.NewString()
		require.NoError(t, store.Set(ctx, storageKey(corruptSubject), []byte(`{"schema_version":2}`)))
		_, err = repository.Consume(ctx, corruptSubject, 1, time.Minute)
		require.True(t, errors.Is(err, ErrCorruptRecord))

		concurrentSubject := "concurrent:" + uuid.NewString()
		counts := make([]int, 32)
		consumeErrors := make([]error, len(counts))
		var wg sync.WaitGroup
		for index := range counts {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				decision, consumeErr := repository.Consume(ctx, concurrentSubject, 16, time.Minute)
				consumeErrors[index] = consumeErr
				counts[index] = int(decision.Count)
			}(index)
		}
		wg.Wait()
		for _, consumeErr := range consumeErrors {
			require.NoError(t, consumeErr)
		}
		sort.Ints(counts)
		for index := range counts {
			require.Equal(t, index+1, counts[index])
		}
	})
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}
