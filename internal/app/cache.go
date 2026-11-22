package app

import (
	"context"
	"fmt"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/document"
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

// Extraction caching has an independent capacity and lifecycle from response caching.
func (b *runtimeBuilder) openExtractionCache() (*document.Cache, error) {
	if !b.config.Cache.Enabled || !b.config.Cache.ExtractionsEnabled {
		return nil, nil
	}
	store, err := cache.NewBufferedLocalCache(16, document.DefaultCacheWindow)
	if err != nil {
		return nil, fmt.Errorf("open extraction byte cache: %w", err)
	}
	extractions, err := document.NewCache(store, nil, 0)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("open extraction cache: %w", err)
	}
	b.application.extractionCache = store
	b.application.own("extraction cache", func(context.Context) error { return store.Close() })
	return extractions, nil
}
