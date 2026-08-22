package view

import (
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// Endpoints projects every chat-completions endpoint that can serve the
// model, matched by route id or definition id. The result is empty, not
// nil, when nothing matches, so the wire shape keeps its array.
func Endpoints(
	snapshot *runtimecatalog.RoutableSnapshot,
	modelID string,
) []EndpointInfo {
	endpoints := make([]EndpointInfo, 0)
	if snapshot == nil {
		return endpoints
	}
	for _, route := range snapshot.Routes() {
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
