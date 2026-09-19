package catalog

import (
	"slices"

	"github.com/agentstation/starmap/pkg/catalogs"
)

func (s *RoutableSnapshot) buildRouteIndexes() {
	s.catalogNames = make(map[string]struct{})
	for _, entry := range s.discoveryIndex {
		s.catalogNames[string(entry.definition)] = struct{}{}
		for _, key := range entry.offerings {
			s.catalogNames[string(key.ProviderID)+"/"+string(key.ProviderModelID)] = struct{}{}
		}
	}
	s.routesByID = make(map[string]int, len(s.routes))
	s.routesByDefinition = make(map[catalogs.ModelDefinitionID][]int)
	s.routesByProvider = make(map[catalogs.ProviderID][]int)
	for index, route := range s.routes {
		s.routesByID[route.ID()] = index
		if _, found := s.routesByDefinition[route.DefinitionID]; !found {
			s.routableDefinitions = append(s.routableDefinitions, route.DefinitionID)
		}
		s.routesByDefinition[route.DefinitionID] = append(s.routesByDefinition[route.DefinitionID], index)
		s.routesByProvider[route.ProviderID] = append(s.routesByProvider[route.ProviderID], index)
	}
}

func (s *RoutableSnapshot) copyIndexedRoutes(indexes []int) []Route {
	routes := make([]Route, len(indexes))
	for i, index := range indexes {
		routes[i] = cloneRoute(s.routes[index])
	}
	return routes
}

// RoutesForModel returns caller-owned routes matching a canonical or exact offering name.
func (s *RoutableSnapshot) RoutesForModel(name string) []Route {
	if s == nil {
		return nil
	}
	resolved, valid := s.ResolveAlias(name)
	if !valid {
		return []Route{}
	}
	return s.copyIndexedRoutes(s.matchingRouteIndexes(resolved))
}

func (s *RoutableSnapshot) matchingRouteIndexes(name string) []int {
	indexes := s.routesByDefinition[catalogs.ModelDefinitionID(name)]
	exact, found := s.routesByID[name]
	if !found || string(s.routes[exact].DefinitionID) == name {
		return indexes
	}
	result := append(slices.Clone(indexes), exact)
	slices.Sort(result)
	return result
}
