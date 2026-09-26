package proxy

import (
	"context"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
)

type discoverySnapshotKey struct{}

// bindDiscoverySnapshot keeps the cache key and projection on the same view.
// Permission checks remain live through the snapshot's authority.
func bindDiscoverySnapshot(ctx context.Context, runtime connectors.RuntimeLease) context.Context {
	if _, ok := ctx.Value(discoverySnapshotKey{}).(*runtimecatalog.RoutableSnapshot); ok || runtime == nil {
		return ctx
	}
	return context.WithValue(ctx, discoverySnapshotKey{}, runtime.Snapshot())
}

func discoverySnapshot(ctx context.Context, runtime connectors.RuntimeLease) *runtimecatalog.RoutableSnapshot {
	if snapshot, ok := ctx.Value(discoverySnapshotKey{}).(*runtimecatalog.RoutableSnapshot); ok {
		return snapshot
	}
	if runtime == nil {
		return nil
	}
	return runtime.Snapshot()
}
