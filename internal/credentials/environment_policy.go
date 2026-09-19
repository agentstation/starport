package credentials

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// EnvironmentPolicy identifies a versioned inference credential selection order.
type EnvironmentPolicy string

const (
	// InferencePolicyLegacy preserves conventional-first selection for upgrade comparison.
	InferencePolicyLegacy EnvironmentPolicy = "starport-inference-v1"
	// InferencePolicyCurrent selects gateway names before conventional names.
	InferencePolicyCurrent EnvironmentPolicy = "starport-inference-v2"
)

// SelectionPolicyStore retains accepted policy versions without credential material.
type SelectionPolicyStore interface {
	Policy(context.Context, catalogs.ProviderID) (EnvironmentPolicy, error)
	Accept(context.Context, catalogs.ProviderID) error
}

// WithEnvironmentPolicy selects the order used by this resolver.
func WithEnvironmentPolicy(policy EnvironmentPolicy) ResolverOption {
	return func(r *Resolver) { r.environmentPolicy = policy }
}

// WithStarmapFallback explicitly permits Starmap names after ordinary inference names.
func WithStarmapFallback(enabled bool) ResolverOption {
	return func(r *Resolver) { r.starmapFallback = enabled }
}

// WithSelectionPolicyStore requires migration checks before caching newly selected material.
func WithSelectionPolicyStore(store SelectionPolicyStore) ResolverOption {
	return func(r *Resolver) { r.selectionPolicy = store }
}

func (r *Resolver) environmentCandidates(provider catalogs.ProviderID, field catalogs.ProviderCredentialField) ([]string, error) {
	product, err := catalogs.DerivedCredentialEnvironmentName(starportCredentialProduct, provider, field.ID)
	if err != nil {
		return nil, err
	}
	var names []string
	switch r.environmentPolicy {
	case InferencePolicyLegacy:
		names = append(append([]string(nil), field.Environment...), product)
	case InferencePolicyCurrent:
		names = append([]string{product}, field.Environment...)
	default:
		return nil, fmt.Errorf("unsupported inference credential policy")
	}
	if r.starmapFallback && r.environmentPolicy == InferencePolicyCurrent {
		inherited, err := catalogs.DerivedCredentialEnvironmentName("STARMAP", provider, field.ID)
		if err != nil {
			return nil, err
		}
		names = append(names, inherited)
	}
	return names, nil
}

func (r *Resolver) emptySelection(value string) bool {
	if r.environmentPolicy == InferencePolicyCurrent {
		return strings.TrimSpace(value) == ""
	}
	return value == ""
}

// ForSelectionPolicy creates a fresh resolver cache with the same source clients.
// Startup uses it before it exposes any provider material to request routing.
func (r *Resolver) ForSelectionPolicy(store SelectionPolicyStore, allowStarmap bool) *Resolver {
	selected := NewResolver(WithEnvironmentLookup(r.lookup), WithEnvironmentPolicy(InferencePolicyCurrent), WithStarmapFallback(allowStarmap), WithSelectionPolicyStore(store), WithResolverClock(r.now), WithDirectSecretRefreshInterval(r.directSecretRefreshInterval))
	for key, source := range r.sources {
		selected.sources[key] = source
	}
	for key, chain := range r.cloudChains {
		selected.cloudChains[key] = chain
	}
	return selected
}
