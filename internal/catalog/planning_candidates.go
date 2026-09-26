package catalog

import (
	"maps"
	"slices"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/routing"
)

func (s *RoutableSnapshot) buildPlanningCandidates() {
	routes := s.routes
	candidates := make([]routing.Candidate, 0, len(routes))
	for _, route := range routes {
		definition, err := s.Definition(route.DefinitionID)
		if err != nil {
			continue
		}
		offering, err := s.Offering(route)
		if err != nil {
			continue
		}
		contextWindow := 0
		maxDocuments := 0
		if offering.Limits != nil {
			if offering.Limits.ContextWindow > 0 {
				contextWindow = boundedInt(offering.Limits.ContextWindow)
			}
			// A document bound belongs to the offering, and two offerings of
			// one model state different ones. Carrying it here is what lets the
			// rerank path refuse a list the chosen provider would reject.
			if offering.Limits.MaxDocuments > 0 {
				maxDocuments = boundedInt(offering.Limits.MaxDocuments)
			}
		}
		provider := string(route.ProviderID)
		candidates = append(candidates, routing.Candidate{
			Route: routing.Route{
				CatalogGenerationID: route.CatalogGenerationID,
				ModelID:             string(route.DefinitionID),
				ProviderID:          provider,
				ProviderModelID:     string(route.ProviderModelID),
			},
			Operations:      planningOperations(route.Operations),
			Endpoints:       planningEndpoints(route.Endpoints),
			PromptCache:     copyPlanningBool(route.PromptCache),
			Capabilities:    modelCapabilities(definition),
			InputModalities: modelInputModalities(definition),
			ContextWindow:   contextWindow,
			MaxDocuments:    maxDocuments,
			Cost:            modelCost(offering.Pricing),
		})
	}
	s.planningCandidates = candidates
	s.planningByModel = make(map[string][]int)
	for index, candidate := range candidates {
		for _, name := range []string{candidate.Route.ModelID, candidate.Route.ID()} {
			s.planningByModel[name] = append(s.planningByModel[name], index)
		}
	}
}

// PlanningCandidates returns caller-owned static candidates for the selected names.
// An unrestricted selection returns every candidate in snapshot order.
func (s *RoutableSnapshot) PlanningCandidates(names []string, unrestricted bool) []routing.Candidate {
	if s == nil {
		return nil
	}
	if unrestricted {
		result := make([]routing.Candidate, len(s.planningCandidates))
		for i, candidate := range s.planningCandidates {
			result[i] = clonePlanningCandidate(candidate)
		}
		return result
	}
	indexes := make([]int, 0, len(names))
	seen := make(map[int]struct{})
	for _, name := range names {
		for _, index := range s.planningByModel[name] {
			if _, exists := seen[index]; exists {
				continue
			}
			seen[index] = struct{}{}
			indexes = append(indexes, index)
		}
	}
	slices.Sort(indexes)
	result := make([]routing.Candidate, len(indexes))
	for i, index := range indexes {
		result[i] = clonePlanningCandidate(s.planningCandidates[index])
	}
	return result
}

func clonePlanningCandidate(candidate routing.Candidate) routing.Candidate {
	candidate.Operations = slices.Clone(candidate.Operations)
	candidate.Endpoints = maps.Clone(candidate.Endpoints)
	candidate.PromptCache = copyPlanningBool(candidate.PromptCache)
	candidate.Capabilities = slices.Clone(candidate.Capabilities)
	candidate.InputModalities = slices.Clone(candidate.InputModalities)
	if candidate.Cost != nil {
		candidate.Cost = new(*candidate.Cost)
	}
	return candidate
}

func planningOperations(operations []starmapcatalogs.ProviderOperation) []routing.Operation {
	result := make([]routing.Operation, len(operations))
	for index, operation := range operations {
		result[index] = routing.Operation(operation)
	}
	return result
}

func planningEndpoints(endpoints []starmapcatalogs.ProviderOfferingEndpoint) map[routing.Operation]routing.Endpoint {
	result := make(map[routing.Operation]routing.Endpoint, len(endpoints))
	for _, endpoint := range endpoints {
		result[routing.Operation(endpoint.Operation)] = routing.Endpoint{
			Protocol:  string(endpoint.Type),
			URL:       endpoint.URL,
			StreamURL: endpoint.StreamURL,
		}
	}
	return result
}

func copyPlanningBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func boundedInt(value int64) int {
	maxInt := int64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func modelCapabilities(definition starmapcatalogs.ModelDefinition) []string {
	features := definition.Capabilities.Features
	if features == nil {
		return nil
	}
	capabilities := make([]string, 0, 6)
	if features.Tools || features.ToolCalls {
		capabilities = append(capabilities, "function_calling", "tools")
	}
	for _, modality := range features.Modalities.Input {
		if modality == starmapcatalogs.ModelModalityImage {
			capabilities = append(capabilities, "vision")
			break
		}
	}
	if features.Reasoning {
		capabilities = append(capabilities, "reasoning")
	}
	if features.StructuredOutputs {
		capabilities = append(capabilities, "structured_outputs")
	}
	if features.Streaming {
		capabilities = append(capabilities, "streaming")
	}
	return capabilities
}

// modelInputModalities maps catalog input modalities to planner modalities.
// It omits modalities that the planner does not support.
func modelInputModalities(definition starmapcatalogs.ModelDefinition) []routing.Modality {
	features := definition.Capabilities.Features
	if features == nil {
		return nil
	}
	modalities := make([]routing.Modality, 0, len(features.Modalities.Input))
	for _, modality := range features.Modalities.Input {
		if planned, known := planningModality(modality); known {
			modalities = append(modalities, planned)
		}
	}
	if len(modalities) == 0 {
		return nil
	}
	return modalities
}

// planningModality translates one catalog modality. Starmap records a
// document as the pdf modality, and this boundary is the only place that
// translation lives.
func planningModality(modality starmapcatalogs.ModelModality) (routing.Modality, bool) {
	switch modality {
	case starmapcatalogs.ModelModalityText:
		return routing.ModalityText, true
	case starmapcatalogs.ModelModalityImage:
		return routing.ModalityImage, true
	case starmapcatalogs.ModelModalityAudio:
		return routing.ModalityAudio, true
	case starmapcatalogs.ModelModalityVideo:
		return routing.ModalityVideo, true
	case starmapcatalogs.ModelModalityPDF:
		return routing.ModalityDocument, true
	}
	return "", false
}

func modelCost(pricing *starmapcatalogs.ModelPricing) *routing.TokenCost {
	if pricing == nil || pricing.Tokens == nil {
		return nil
	}
	cost := &routing.TokenCost{}
	known := false
	if pricing.Tokens.Input != nil {
		cost.InputPerToken = planningTokenPrice(pricing.Tokens.Input)
		known = true
	}
	if pricing.Tokens.Output != nil {
		cost.OutputPerToken = planningTokenPrice(pricing.Tokens.Output)
		known = true
	}
	if !known {
		return nil
	}
	return cost
}

func planningTokenPrice(cost *starmapcatalogs.ModelTokenCost) float64 {
	if cost == nil {
		return 0
	}
	if cost.PerToken != 0 {
		return cost.PerToken
	}
	return cost.Per1M / 1_000_000
}
