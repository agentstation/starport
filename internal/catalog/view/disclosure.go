package view

import runtimecatalog "github.com/agentstation/starport/internal/catalog"

func permittedRoutes(routes []runtimecatalog.Route, policy runtimecatalog.DisclosurePolicy) []runtimecatalog.Route {
	if policy == nil {
		return routes
	}
	result := routes[:0]
	for _, route := range routes {
		if policy.AllowsDefinition(route.DefinitionID) && policy.AllowsOffering(route.Key()) {
			result = append(result, route)
		}
	}
	return result
}
