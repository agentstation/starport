package view

import (
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/disclosure"
)

// Endpoints projects every chat-completions endpoint that can serve the
// model, matched by route id or definition id. The result is empty, not
// nil, when nothing matches, so the wire shape keeps its array.
func Endpoints(
	snapshot *runtimecatalog.RoutableSnapshot,
	modelID string,
) []EndpointInfo {
	return endpoints(snapshot, modelID, nil)
}

// EndpointsForViewer projects permitted endpoints for a model.
func EndpointsForViewer(snapshot *runtimecatalog.RoutableSnapshot, modelID string, policy disclosure.Policy) []EndpointInfo {
	return endpoints(snapshot, modelID, policy)
}

func endpoints(snapshot *runtimecatalog.RoutableSnapshot, modelID string, policy runtimecatalog.DisclosurePolicy) []EndpointInfo {
	endpoints := make([]EndpointInfo, 0)
	if snapshot == nil {
		return endpoints
	}
	var valid bool
	modelID, valid = snapshot.ResolveAlias(modelID)
	if !valid {
		return endpoints
	}
	for _, route := range permittedRoutes(snapshot.Routes(), policy) {
		if route.ID() != modelID && string(route.DefinitionID) != modelID {
			continue
		}
		endpoint, found := route.Endpoint(starmapcatalogs.ProviderOperationChatCompletions)
		if !found {
			continue
		}
		info := EndpointInfo{
			Provider: string(route.ProviderID), Endpoint: endpoint.URL, Available: true,
		}
		offering, err := snapshot.Offering(route)
		if err == nil && offering.Pricing != nil && offering.Pricing.Tokens != nil {
			info.CostPrompt = formatTokenPrice(offering.Pricing.Tokens.Input)
			info.CostOutput = formatTokenPrice(offering.Pricing.Tokens.Output)
		}
		endpoints = append(endpoints, info)
	}
	return endpoints
}
