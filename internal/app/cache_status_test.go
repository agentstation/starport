package app

import (
	"encoding/json/v2"
	"testing"

	"github.com/agentstation/starport/internal/cache"
	"github.com/stretchr/testify/require"
)

func TestCacheStatusPreservesOperatorFields(t *testing.T) {
	status := cache.FillStatus{
		Shared:  cache.SharedStatus{Configured: true, Available: true, State: "ready", KeyPrefix: "deployment"},
		Enabled: true, EntryLimit: 1, ByteLimit: 2, WorkerLimit: 3,
		RetainedEntries: 4, RetainedBytes: 5, ActiveFills: 6,
		DroppedFills: 7, FailedFills: 8, CompletedFills: 9,
	}
	before, err := json.Marshal(status)
	require.NoError(t, err)
	after, err := json.Marshal(cacheStatus(status))
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}
