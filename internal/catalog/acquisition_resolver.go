package catalog

import (
	"context"

	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/pkg/sources"
)

// DeploymentLookup reads deployment configuration without account or BYOK access.
type DeploymentLookup func(name string) (string, bool)

// AcquisitionResolver uses Starmap's deployment credential sources and policy.
type AcquisitionResolver struct {
	resolver sources.ProviderCredentialResolver
	err      error
	disabled bool
}

// NewAcquisitionResolver creates an ephemeral acquisition resolver without reading secrets.
// A nil lookup refuses resolution and never falls back to the process environment.
func NewAcquisitionResolver(lookup DeploymentLookup) *AcquisitionResolver {
	return newAcquisitionResolver(context.Background(), lookup, nil)
}

func newAcquisitionResolver(ctx context.Context, lookup DeploymentLookup, state *acquisition.CredentialPolicyState) *AcquisitionResolver {
	disabled := lookup == nil
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	resolver, err := acquisition.OpenCredentialResolver(ctx, acquisition.CredentialResolverConfig{
		Product: acquisition.CredentialProductStarport, Lookup: lookup, State: state,
	})
	return &AcquisitionResolver{resolver: resolver, err: err, disabled: disabled}
}

// ResolveCatalog resolves acquisition material without inference or account stores.
func (r *AcquisitionResolver) ResolveCatalog(ctx context.Context, provider *catalogs.Provider) (sources.ProviderCredentialMaterial, error) {
	if r == nil || r.disabled || r.resolver == nil && r.err == nil {
		return sources.ProviderCredentialMaterial{}, &starmaperrors.ConfigError{Component: "catalog acquisition", Message: "credential resolver is required"}
	}
	if r.err != nil {
		return sources.ProviderCredentialMaterial{}, r.err
	}
	return r.resolver.ResolveCatalog(ctx, provider)
}

var _ sources.ProviderCredentialResolver = (*AcquisitionResolver)(nil)
