package cache

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestModelFillOversizeHasBoundedEncoding(t *testing.T) {
	manager, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	model := map[string]string{"description": strings.Repeat("x", 4<<20)}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	require.NoError(t, manager.SetModel(t.Context(), "oversized", model))
	runtime.ReadMemStats(&after)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20), "a rejected large string must not allocate its encoded copy")
	require.Equal(t, uint64(1), manager.FillStatus().DroppedFills)
}

type modelString string

type modelBytes []byte

func TestModelEncodingPreservesJSONAndBounds(t *testing.T) {
	for _, value := range []any{
		map[string]any{"nil": []byte(nil), "bytes": []byte{0, 1, 255}, "html": "<>&", "integer": int64(9007199254740993)},
		modelString("<>&"), modelBytes{0, 1, 255},
	} {
		want, err := json.Marshal(value)
		require.NoError(t, err)
		got, err := encodeModel(t.Context(), value, fillQueueBytes)
		require.NoError(t, err)
		require.JSONEq(t, string(want), string(got))
	}
	for _, value := range []any{strings.Repeat("x", modelScalarLimit+1), modelString(strings.Repeat("x", modelScalarLimit+1)), make([]byte, modelScalarLimit+1), modelBytes(make([]byte, modelScalarLimit+1)), make([]int, modelContainerLimit+1)} {
		got, err := encodeModel(t.Context(), value, fillQueueBytes)
		require.ErrorIs(t, err, errModelEncodingLimit)
		require.Nil(t, got)
	}
	got, err := encodeModel(t.Context(), []string{"first", "second"}, 8)
	require.ErrorIs(t, err, errModelEncodingLimit)
	require.Nil(t, got)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = encodeModel(ctx, "value", 128)
	require.ErrorIs(t, err, context.Canceled)
}

func TestModelCacheTypedDecodePreservesInteger(t *testing.T) {
	manager, err := NewCacheManager(ManagerConfig{}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	type model struct {
		Limit int64 `json:"limit"`
	}
	want := model{Limit: 9007199254740993}
	require.NoError(t, manager.SetModel(t.Context(), "model", want))
	var got model
	require.Eventually(t, func() bool { found, err := manager.GetModel(t.Context(), "model", &got); return err == nil && found }, time.Second, time.Millisecond)
	require.Equal(t, want, got)
}

func TestModelEncodingRefusesPointerCycle(t *testing.T) {
	var cycle any
	cycle = &cycle
	_, err := encodeModel(t.Context(), cycle, fillQueueBytes)
	require.ErrorIs(t, err, errModelEncodingLimit)
}

func TestOwnedModelFillChargesCapacity(t *testing.T) {
	local, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	defer local.Close()
	queue := newFillQueue(local)
	defer queue.close()
	payload := make([]byte, 1, fillQueueBytes+1)
	require.NoError(t, queue.enqueueOwnedTo(t.Context(), local, "key", payload, time.Minute))
	require.Equal(t, uint64(1), queue.stats().DroppedFills)
	require.Zero(t, queue.stats().RetainedBytes)
}
