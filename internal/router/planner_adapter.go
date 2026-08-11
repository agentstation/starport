package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

func (r *modelRouter) planRoute(
	ctx context.Context,
	req *Request,
	runtime connectors.RuntimeLease,
) (*routing.Plan, error) {
	if r.availability != nil {
		r.availability.Refresh(ctx)
	}
	if runtime == nil || runtime.Snapshot() == nil {
		return r.planRegistryRoute(ctx, req, runtime)
	}

	snapshot := runtime.Snapshot()
	if snapshot == nil {
		return nil, ErrNoModelsAvailable
	}
	request := r.toPlanningRequest(req)
	request.Operation = routing.OperationChatCompletions
	request.Models, request.AllowAnyModelFallback = splitAutoModel(request.Models)
	request.AllowModelFallbacks = len(request.Models) > 1
	input := routing.Snapshot{
		CatalogGenerationID:  snapshot.GenerationID(),
		AvailabilityRevision: snapshot.AvailabilityRevision(),
		Candidates:           r.toPlanningCandidates(snapshot, runtime),
	}
	return r.routePlanner.Plan(request, input)
}

func splitAutoModel(models []string) ([]string, bool) {
	explicit := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	allowAny := false
	for _, modelID := range models {
		if modelID == AutoModelID {
			allowAny = true
			continue
		}
		if _, duplicate := seen[modelID]; duplicate {
			continue
		}
		seen[modelID] = struct{}{}
		explicit = append(explicit, modelID)
	}
	return explicit, allowAny
}

func (r *modelRouter) planRegistryRoute(
	_ context.Context,
	req *Request,
	runtime connectors.RuntimeLease,
) (*routing.Plan, error) {
	models := r.getCandidateModels(req)
	if req != nil {
		models = r.filterByProviderPreferences(models, req.ProviderPreferences)
		if req.APIKeyConfig != nil {
			models = r.filterByAPIKeyRestrictions(models, req.APIKeyConfig)
		}
	}
	if len(models) == 0 {
		return nil, ErrNoModelsAvailable
	}
	if req != nil && req.Metadata != nil && req.Metadata.ConversationID != "" && r.config.EnableStickySessions {
		if provider, exists := r.stickyProviderSessionManager.GetProvider(req.Metadata.ConversationID); exists {
			models = moveProviderFirst(models, provider, r.extractProvider)
		}
	}

	attempts := make([]routing.Attempt, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, modelID := range models {
		provider := r.extractProvider(modelID)
		if provider == "" || runtime == nil || runtime.Get(provider) == nil {
			continue
		}
		providerModelID := modelID
		if _, modelName, ok := runtimecatalog.SplitModelID(modelID); ok {
			providerModelID = modelName
		}
		route := routing.Route{
			CatalogGenerationID: "registry-runtime",
			ModelID:             modelID,
			ProviderID:          provider,
			ProviderModelID:     providerModelID,
		}
		if _, duplicate := seen[route.ID()]; duplicate {
			continue
		}
		seen[route.ID()] = struct{}{}
		attempts = append(attempts, routing.Attempt{Route: route})
	}
	if len(attempts) == 0 {
		return nil, fmt.Errorf("%w: no registered provider adapter", ErrNoModelsAvailable)
	}
	return routing.NewPlan("registry-runtime", 0, attempts, nil)
}

func moveProviderFirst(models []string, provider string, providerFor func(string) string) []string {
	preferred := make([]string, 0, len(models))
	remaining := make([]string, 0, len(models))
	for _, modelID := range models {
		if providerFor(modelID) == provider {
			preferred = append(preferred, modelID)
		} else {
			remaining = append(remaining, modelID)
		}
	}
	return append(preferred, remaining...)
}

func (r *modelRouter) toPlanningRequest(req *Request) routing.Request {
	var models []string
	if req != nil {
		models = r.getCandidateModels(req)
	}
	request := routing.Request{
		Models:              append([]string(nil), models...),
		AllowModelFallbacks: len(models) > 1,
		Optimization: routing.OptimizationPolicy{
			PreferLowestCost:    r.config.EnableCostOptimization,
			PreferLowestLatency: true,
		},
	}
	if req == nil {
		return request
	}
	if req.Metadata != nil {
		request.RequiredCapabilities = append([]string(nil), req.Metadata.RequiredFeatures...)
		request.EstimatedInputTokens = req.Metadata.EstimatedTokens
		request.EstimatedOutputTokens = req.Metadata.EstimatedTokens / 4
		if r.config.EnableStickySessions && req.Metadata.ConversationID != "" {
			if provider, exists := r.stickyProviderSessionManager.GetProvider(req.Metadata.ConversationID); exists {
				request.AffinityProvider = provider
			}
		}
	}
	if req.ChatRequest != nil && req.MaxTokens != nil {
		request.EstimatedOutputTokens = *req.MaxTokens
	}
	request.RequiredContextTokens = request.EstimatedInputTokens + request.EstimatedOutputTokens
	if req.ProviderPreferences != nil {
		request.Providers = routing.ProviderPolicy{
			Order:          normalizeProviders(req.ProviderPreferences.Order),
			Only:           normalizeProviders(req.ProviderPreferences.Only),
			Ignore:         normalizeProviders(req.ProviderPreferences.Ignore),
			AllowFallbacks: req.ProviderPreferences.AllowFallbacks,
		}
	}
	if req.APIKeyConfig != nil {
		request.Tenant = routing.TenantPolicy{
			AllowedModels:    wildcardAsUnrestricted(req.APIKeyConfig.AllowedModels),
			AllowedProviders: normalizeProviders(req.APIKeyConfig.AllowedProviders),
			ModelOverrides:   cloneModelOverrides(req.APIKeyConfig.ModelOverrides),
		}
	}
	return request
}

func cloneModelOverrides(overrides map[string]string) map[string]string {
	if len(overrides) == 0 {
		return nil
	}
	result := make(map[string]string, len(overrides))
	for modelID, override := range overrides {
		result[modelID] = override
	}
	return result
}

func (r *modelRouter) toPlanningCandidates(
	snapshot *runtimecatalog.RoutableSnapshot,
	runtime connectors.RuntimeLease,
) []routing.Candidate {
	routes := snapshot.Routes()
	providerStates := make(map[string]providerPlanningState)
	for _, route := range routes {
		provider := string(route.ProviderID)
		if _, exists := providerStates[provider]; exists {
			continue
		}
		providerStates[provider] = providerPlanningState{
			latency:     measuredProviderLatency(r.latencyTracker, provider),
			unavailable: runtime == nil || runtime.Get(provider) == nil,
		}
	}
	candidates := make([]routing.Candidate, 0, len(routes))
	for _, route := range routes {
		definition, err := snapshot.Definition(route.DefinitionID)
		if err != nil {
			continue
		}
		offering, err := snapshot.Offering(route)
		if err != nil {
			continue
		}
		contextWindow := 0
		if offering.Limits != nil && offering.Limits.ContextWindow > 0 {
			contextWindow = boundedInt(offering.Limits.ContextWindow)
		}
		provider := string(route.ProviderID)
		providerState := providerStates[provider]
		candidates = append(candidates, routing.Candidate{
			Route: routing.Route{
				CatalogGenerationID: route.CatalogGenerationID,
				ModelID:             string(route.DefinitionID),
				ProviderID:          provider,
				ProviderModelID:     string(route.ProviderModelID),
			},
			Operations:    planningOperations(route.Operations),
			Endpoints:     planningEndpoints(route.Endpoints),
			PromptCache:   copyPlanningBool(route.PromptCache),
			Capabilities:  modelCapabilities(definition),
			ContextWindow: contextWindow,
			Cost:          modelCost(offering.Pricing),
			Latency:       providerState.latency,
			Unavailable:   providerState.unavailable,
		})
	}
	return candidates
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

type providerPlanningState struct {
	latency     *time.Duration
	unavailable bool
}

func boundedInt(value int64) int {
	maxInt := int64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func normalizeProviders(providers []string) []string {
	return append([]string(nil), providers...)
}

func wildcardAsUnrestricted(models []string) []string {
	for _, model := range models {
		if model == "*" {
			return nil
		}
	}
	return append([]string(nil), models...)
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

func modelCost(pricing *starmapcatalogs.ModelPricing) *routing.TokenCost {
	if pricing == nil || pricing.Tokens == nil {
		return nil
	}
	cost := &routing.TokenCost{}
	known := false
	if pricing.Tokens.Input != nil {
		cost.InputPerToken = tokenPrice(pricing.Tokens.Input)
		known = true
	}
	if pricing.Tokens.Output != nil {
		cost.OutputPerToken = tokenPrice(pricing.Tokens.Output)
		known = true
	}
	if !known {
		return nil
	}
	return cost
}

func tokenPrice(cost *starmapcatalogs.ModelTokenCost) float64 {
	if cost == nil {
		return 0
	}
	if cost.PerToken != 0 {
		return cost.PerToken
	}
	return cost.Per1M / 1_000_000
}

func measuredProviderLatency(tracker LatencyTracker, provider string) *time.Duration {
	if tracker == nil || strings.TrimSpace(provider) == "" {
		return nil
	}
	latency := tracker.GetLatency(provider)
	if latency <= 0 {
		return nil
	}
	return &latency
}
