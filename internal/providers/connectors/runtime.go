package connectors

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
)

// RuntimeLease retains one complete provider runtime generation for a request.
type RuntimeLease interface {
	Snapshot() *runtimecatalog.RoutableSnapshot
	Get(string) Connector
	ResolveMaterial(context.Context, string) (credentials.Material, error)
	Release()
}

// AnonymousMaterialSource supplies a catalog-default no-auth profile without
// reading operator credential state.
type AnonymousMaterialSource interface {
	AnonymousMaterial(string) (credentials.Material, bool)
}

// EndpointBinder binds one catalog endpoint after request material selection.
type EndpointBinder interface {
	BindEndpoint(
		string,
		catalogs.ProviderOfferingEndpoint,
		credentials.Material,
		bool,
	) (catalogs.ProviderOfferingEndpoint, error)
}

// LeasingRegistry supplies complete provider runtime generations.
type LeasingRegistry interface {
	AcquireRuntime() (RuntimeLease, error)
}

type runtimeLeaseContextKey struct{}

// ContextWithRuntimeLease binds one already-owned runtime lease to a request.
// The caller that acquired the lease remains responsible for releasing it.
func ContextWithRuntimeLease(ctx context.Context, lease RuntimeLease) context.Context {
	if ctx == nil || lease == nil {
		return ctx
	}
	return context.WithValue(ctx, runtimeLeaseContextKey{}, lease)
}

// RuntimeLeaseFromContext returns the request's borrowed runtime lease.
func RuntimeLeaseFromContext(ctx context.Context) RuntimeLease {
	if ctx == nil {
		return nil
	}
	lease, _ := ctx.Value(runtimeLeaseContextKey{}).(RuntimeLease)
	return lease
}
