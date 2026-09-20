package app

import (
	"context"
	"fmt"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/document"
)

// Extraction caching has an independent capacity and lifecycle from response caching.
func (b *runtimeBuilder) openExtractionCache() (*document.Cache, error) {
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
