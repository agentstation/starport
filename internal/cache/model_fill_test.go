package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type delayedModel struct{ started, release chan struct{} }

func (m delayedModel) MarshalJSON() ([]byte, error) {
	close(m.started)
	<-m.release
	return []byte(`{"id":"stale"}`), nil
}

func TestModelFillCannotCrossInvalidation(t *testing.T) {
	manager, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	model := delayedModel{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- manager.SetModel(t.Context(), "model", model) }()
	<-model.started
	manager.InvalidateModels()
	close(model.release)
	require.NoError(t, <-done)
	require.Eventually(t, func() bool { return manager.fills.stats().RetainedEntries == 0 }, time.Second, time.Millisecond)
	var decoded any
	found, err := manager.GetModel(t.Context(), "model", &decoded)
	require.NoError(t, err)
	require.False(t, found, "a fill started before invalidation must not restore old metadata")
}

func TestModelFillSharesResponseBounds(t *testing.T) {
	local, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	store := &heldFillCache{ResponseStore: local, release: make(chan struct{})}
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	for i := range fillWorkers {
		require.NoError(t, manager.SetResponse(t.Context(), string(rune('a'+i)), []byte("held")))
		require.Eventually(t, func() bool { return manager.fills.stats().ActiveFills == int64(i+1) }, time.Second, time.Millisecond)
	}
	require.NoError(t, manager.SetModel(t.Context(), "model", map[string]string{"id": "queued"}))
	require.Equal(t, int64(fillWorkers+1), manager.fills.stats().RetainedEntries)
	var decoded any
	found, err := manager.GetModel(t.Context(), "model", &decoded)
	require.NoError(t, err)
	require.False(t, found, "optional model fill must wait in the bounded queue")
	close(store.release)
	require.Eventually(t, func() bool {
		var decoded any
		found, err := manager.GetModel(t.Context(), "model", &decoded)
		return err == nil && found
	}, time.Second, time.Millisecond)
}
