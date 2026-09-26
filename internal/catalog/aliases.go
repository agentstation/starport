package catalog

import (
	"fmt"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// ResolveAlias resolves a retained canonical alias without reading external state.
// Ordinary names pass through. Removed aliases and missing snapshots return false.
func (s *RoutableSnapshot) ResolveAlias(name string) (string, bool) {
	if s == nil || s.catalog == nil {
		return "", false
	}
	target, state, reserved := s.catalog.CanonicalAliases().Lookup(catalogs.ModelDefinitionID(name))
	if !reserved {
		return name, true
	}
	if state != catalogs.CanonicalAliasActive {
		return "", false
	}
	return string(target), true
}

func validateAliasRouteNames(source *catalogs.Catalog) error {
	for _, alias := range source.CanonicalAliasRecords() {
		provider, model, found := strings.Cut(string(alias.ID), "/")
		if !found {
			continue
		}
		if _, err := source.Offering(catalogs.ProviderID(provider), catalogs.ProviderModelID(model)); err == nil {
			return fmt.Errorf("%w: %s", ErrAmbiguousModelName, alias.ID)
		}
	}
	return nil
}
