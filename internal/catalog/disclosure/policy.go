// Package disclosure intersects account and key catalog visibility.
package disclosure

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/catalog"
	"strings"
)

// Policy selects catalog membership permitted by a key and its account.
type Policy struct {
	definitions map[catalogs.ModelDefinitionID]bool
	offerings   map[catalogs.OfferingKey]bool
}

// AllowsDefinition reports permitted canonical membership.
func (p Policy) AllowsDefinition(id catalogs.ModelDefinitionID) bool {
	return p.definitions[id]
}

// AllowsOffering reports permitted provider membership.
func (p Policy) AllowsOffering(key catalogs.OfferingKey) bool { return p.offerings[key] }

// New derives disclosure membership without provider or credential reads.
func New(snapshot *catalog.RoutableSnapshot, key apikey.APIKey, owner account.Account) Policy {
	policy := Policy{definitions: make(map[catalogs.ModelDefinitionID]bool), offerings: make(map[catalogs.OfferingKey]bool)}
	if snapshot == nil || snapshot.Catalog() == nil {
		return policy
	}
	for _, definition := range snapshot.Catalog().Definitions() {
		offerings, err := snapshot.Catalog().DefinitionOfferings(definition.ID)
		if err != nil {
			continue
		}
		if len(offerings) == 0 && len(owner.Access) == 0 && key.CanUseModel(string(definition.ID)) {
			policy.definitions[definition.ID] = true
		}
		for _, offering := range offerings {
			provider, model := string(offering.ProviderID), string(offering.ProviderModelID)
			if !owner.AllowsModel(provider, model) {
				continue
			}
			if !key.CanUseModel(string(definition.ID)) && !key.CanUseModel(provider+"/"+model) {
				continue
			}
			policy.definitions[definition.ID] = true
			policy.offerings[catalogs.OfferingKey{ProviderID: offering.ProviderID, ProviderModelID: offering.ProviderModelID}] = true
		}
	}
	return policy
}

// AllowsName reports permitted canonical or exact provider-scoped membership.
// The caller must resolve aliases within the retained generation first.
func (p Policy) AllowsName(name string) bool {
	if p.AllowsDefinition(catalogs.ModelDefinitionID(name)) {
		return true
	}
	provider, model, found := strings.Cut(name, "/")
	return found && p.AllowsOffering(catalogs.OfferingKey{ProviderID: catalogs.ProviderID(provider), ProviderModelID: catalogs.ProviderModelID(model)})
}
