package router

import (
	"context"
	"errors"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
)

func (r *modelRouter) acquireRuntime(
	ctx context.Context,
) (connectors.RuntimeLease, bool, error) {
	if lease := connectors.RuntimeLeaseFromContext(ctx); lease != nil {
		return lease, false, nil
	}
	if leasing, ok := r.registry.(connectors.LeasingRegistry); ok {
		lease, err := leasing.AcquireRuntime()
		return lease, true, err
	}
	return &legacyRuntimeLease{registry: r.registry, catalog: r.catalog}, true, nil
}

type legacyRuntimeLease struct {
	registry connectors.Registry
	catalog  *runtimecatalog.ControlPlane
}

func (l *legacyRuntimeLease) Snapshot() *runtimecatalog.RoutableSnapshot {
	if l == nil || l.catalog == nil {
		return nil
	}
	return l.catalog.Current()
}

func (l *legacyRuntimeLease) Get(provider string) connectors.Connector {
	if l == nil || l.registry == nil {
		return nil
	}
	return l.registry.Get(provider)
}

func (l *legacyRuntimeLease) ResolveMaterial(
	ctx context.Context,
	provider string,
) (credentials.Material, error) {
	if l == nil || l.registry == nil {
		return credentials.Material{}, errors.New("provider runtime registry is required")
	}
	return l.registry.ResolveMaterial(ctx, provider)
}

func (*legacyRuntimeLease) AnonymousMaterial(string) (credentials.Material, bool) {
	return credentials.Material{}, false
}

func (l *legacyRuntimeLease) BindEndpoint(
	provider string,
	endpoint catalogs.ProviderOfferingEndpoint,
	material credentials.Material,
	_ bool,
) (catalogs.ProviderOfferingEndpoint, error) {
	snapshot := l.Snapshot()
	if snapshot == nil || snapshot.Catalog() == nil {
		return catalogs.ProviderOfferingEndpoint{}, errors.New("provider runtime catalog is required")
	}
	providerRecord, err := snapshot.Catalog().Provider(catalogs.ProviderID(provider))
	if err != nil || providerRecord.Inference == nil {
		return catalogs.ProviderOfferingEndpoint{}, errors.New("provider inference service is required")
	}
	return providerRecord.Inference.BindOfferingEndpoint(endpoint, "", material.EndpointBindings())
}

func (l *legacyRuntimeLease) Release() {}
