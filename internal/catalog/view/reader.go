package view

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/disclosure"
)

// SummaryForViewer counts permitted routable membership in the retained generation.
// The boolean is false when the status belongs to another generation.
func SummaryForViewer(summary runtimecatalog.Summary, snapshot *runtimecatalog.RoutableSnapshot, policy disclosure.Policy) (runtimecatalog.Summary, bool) {
	if snapshot == nil || summary.GenerationID != snapshot.GenerationID() {
		return runtimecatalog.Summary{}, false
	}
	summary.Models = 0
	for _, definition := range snapshot.Definitions() {
		if len(permittedRoutes(snapshot.RoutesForDefinition(definition.ID), policy)) > 0 {
			summary.Models++
		}
	}
	providers := make(map[catalogs.ProviderID]bool)
	for _, route := range permittedRoutes(snapshot.Routes(), policy) {
		providers[route.ProviderID] = true
	}
	summary.Providers = len(providers)
	return summary, true
}

// ChangesForViewer excludes facts outside current permitted membership.
// Removed private records do not become visible through their historical IDs.
func ChangesForViewer(diff runtimecatalog.Diff, snapshot *runtimecatalog.RoutableSnapshot, policy disclosure.Policy) (runtimecatalog.Diff, bool) {
	if snapshot == nil || (diff.Available && diff.ToGenerationID != snapshot.GenerationID()) {
		return runtimecatalog.Diff{}, false
	}
	if !diff.Available {
		return runtimecatalog.Diff{Reason: "fewer than two accepted generations are recorded, so there is nothing to compare yet"}, true
	}
	result := runtimecatalog.Diff{
		Available: true, FromGenerationID: diff.FromGenerationID, ToGenerationID: diff.ToGenerationID,
		FromGeneratedAt: diff.FromGeneratedAt, ToGeneratedAt: diff.ToGeneratedAt,
	}
	for _, id := range diff.ModelsAdded {
		if policy.AllowsDefinition(catalogs.ModelDefinitionID(id)) {
			result.ModelsAdded = append(result.ModelsAdded, id)
		}
	}
	for _, id := range diff.ModelsRemoved {
		if policy.AllowsDefinition(catalogs.ModelDefinitionID(id)) {
			result.ModelsRemoved = append(result.ModelsRemoved, id)
		}
	}
	for _, offering := range diff.OfferingsAdded {
		if permitsOfferingChange(policy, offering) {
			result.OfferingsAdded = append(result.OfferingsAdded, offering)
		}
	}
	for _, offering := range diff.OfferingsRemoved {
		if permitsOfferingChange(policy, offering) {
			result.OfferingsRemoved = append(result.OfferingsRemoved, offering)
		}
	}
	for _, price := range diff.PriceChanges {
		if permitsOfferingChange(policy, runtimecatalog.OfferingChange{Provider: price.Provider, ProviderModelID: price.ProviderModelID, DefinitionID: price.DefinitionID}) {
			result.PriceChanges = append(result.PriceChanges, price)
		}
	}
	result.SemanticallyEqual = len(result.ModelsAdded)+len(result.ModelsRemoved)+len(result.OfferingsAdded)+len(result.OfferingsRemoved)+len(result.PriceChanges) == 0
	return result, true
}

func permitsOfferingChange(policy disclosure.Policy, offering runtimecatalog.OfferingChange) bool {
	return policy.AllowsDefinition(catalogs.ModelDefinitionID(offering.DefinitionID)) && policy.AllowsOffering(catalogs.OfferingKey{ProviderID: catalogs.ProviderID(offering.Provider), ProviderModelID: catalogs.ProviderModelID(offering.ProviderModelID)})
}
