package app

import (
	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/server/controllers"
)

func cacheStatus(status cache.FillStatus) controllers.CacheFillStatus {
	return controllers.CacheFillStatus{
		Shared:  controllers.SharedCacheStatus{Configured: status.Shared.Configured, Available: status.Shared.Available, State: status.Shared.State, KeyPrefix: status.Shared.KeyPrefix},
		Enabled: status.Enabled, EntryLimit: status.EntryLimit, ByteLimit: status.ByteLimit, WorkerLimit: status.WorkerLimit,
		RetainedEntries: status.RetainedEntries, RetainedBytes: status.RetainedBytes, ActiveFills: status.ActiveFills,
		DroppedFills: status.DroppedFills, FailedFills: status.FailedFills, CompletedFills: status.CompletedFills,
	}
}
