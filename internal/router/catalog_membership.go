package router

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/routing"
	"strings"
)

// catalogNamePermitted classifies failed plans against accepted membership.
// Adapter absence must not make a permitted model look removed.
func catalogNamePermitted(snapshot *runtimecatalog.RoutableSnapshot, policy routing.AccountPolicy, name string) bool {
	catalog := snapshot.Catalog()
	permits := func(offering catalogs.ProviderOffering) bool {
		return policy.AllowsRoute(routing.Route{ModelID: string(offering.DefinitionID), ProviderID: string(offering.ProviderID), ProviderModelID: string(offering.ProviderModelID)})
	}
	if _, err := catalog.Definition(catalogs.ModelDefinitionID(name)); err == nil {
		offerings, err := catalog.DefinitionOfferings(catalogs.ModelDefinitionID(name))
		if err != nil {
			return false
		}
		if len(offerings) == 0 {
			return policy.AllowsRoute(routing.Route{ModelID: name})
		}
		for _, offering := range offerings {
			if permits(offering) {
				return true
			}
		}
		return false
	}
	provider, model, found := strings.Cut(name, "/")
	if !found {
		return false
	}
	offering, err := catalog.Offering(catalogs.ProviderID(provider), catalogs.ProviderModelID(model))
	return err == nil && permits(offering)
}
