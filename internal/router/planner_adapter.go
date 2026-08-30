package router

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/telemetry"
)

func (r *modelRouter) planRoute(
	ctx context.Context,
	req *Request,
	runtime connectors.RuntimeLease,
) (*routing.Plan, error) {
	request := r.toPlanningRequest(req)
	return r.planOperation(ctx, request, routing.OperationChatCompletions, runtime, req)
}

func (r *modelRouter) planOperation(
	ctx context.Context,
	request routing.Request,
	operation routing.Operation,
	runtime connectors.RuntimeLease,
	registryRequest *Request,
) (*routing.Plan, error) {
	ctx, span := telemetry.StartSpan(ctx, telemetry.SpanRoutePlan)
	defer span.End()
	if r.availability != nil {
		r.availability.Refresh(ctx)
	}
	if runtime == nil || runtime.Snapshot() == nil {
		if operation == routing.OperationChatCompletions {
			return r.planRegistryRoute(ctx, registryRequest, runtime)
		}
		return nil, ErrNoModelsAvailable
	}

	snapshot := runtime.Snapshot()
	if snapshot == nil {
		return nil, ErrNoModelsAvailable
	}
	request.Operation = operation
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
	models, variants := parseModelVariants(models)
	if len(variants.zeroPriceModels) > 0 {
		// The registry fallback has no catalog price facts, so the ":free"
		// promise cannot be kept. Fail loudly instead of routing to a route
		// that may bill the caller.
		return nil, fmt.Errorf("%w: variant :free needs catalog price facts", ErrNoModelsAvailable)
	}
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
	models, variants := parseModelVariants(models)
	request := routing.Request{
		Models:              models,
		AllowModelFallbacks: len(models) > 1,
		ZeroPriceModels:     variants.zeroPriceModels,
		Optimization:        r.plannerOptimization(req, variants),
	}
	if req == nil {
		return request
	}
	if req.Metadata != nil {
		request.RequiredCapabilities = append([]string(nil), req.Metadata.RequiredFeatures...)
		request.RequiredModalities = planningModalities(req.Metadata.RequiredModalities)
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
			// Wire prices are USD per million tokens; catalog costs are per token.
			MaxPromptPricePerToken:     req.ProviderPreferences.MaxPromptPricePer1M / 1_000_000,
			MaxCompletionPricePerToken: req.ProviderPreferences.MaxCompletionPricePer1M / 1_000_000,
		}
	}
	if req.APIKeyConfig != nil {
		request.Account = routing.AccountPolicy{
			AllowedModels:    wildcardAsUnrestricted(req.APIKeyConfig.AllowedModels),
			AllowedProviders: normalizeProviders(req.APIKeyConfig.AllowedProviders),
			ModelOverrides:   cloneModelOverrides(req.APIKeyConfig.ModelOverrides),
			Access:           cloneProviderAccess(req.APIKeyConfig.Access),
		}
	}
	return request
}

// plannerOptimization resolves the route ordering with defined precedence:
// an explicit provider.sort wins over a model variant suffix, which wins
// over the server default. Starport measures latency, not throughput, so
// "throughput" routes by measured latency. "spread" keeps the default
// ranking and balances traffic inside its leading band; the seed is drawn
// here so the planner stays a pure function of its request.
func (r *modelRouter) plannerOptimization(req *Request, variants variantEffects) routing.OptimizationPolicy {
	requestedSort := ""
	if req != nil && req.ProviderPreferences != nil {
		requestedSort = req.ProviderPreferences.Sort
	}
	switch {
	case requestedSort == "price":
		return routing.OptimizationPolicy{PreferLowestCost: true}
	case requestedSort == "latency" || requestedSort == "throughput":
		return routing.OptimizationPolicy{PreferLowestLatency: true}
	case requestedSort == "spread":
		return routing.OptimizationPolicy{
			PreferLowestCost:    r.config.EnableCostOptimization,
			PreferLowestLatency: true,
			Spread:              true,
			SpreadSeed:          rand.Uint64(), // #nosec G404 -- route spread is load balancing, not a secret.
		}
	case variants.sortPrice:
		return routing.OptimizationPolicy{PreferLowestCost: true}
	case variants.sortLatency:
		return routing.OptimizationPolicy{PreferLowestLatency: true}
	}
	return routing.OptimizationPolicy{
		PreferLowestCost:    r.config.EnableCostOptimization,
		PreferLowestLatency: true,
	}
}

// cloneProviderAccess copies the paired provider and model grants so the
// planner's view cannot alias the caller's slice.
func cloneProviderAccess(access []routing.ProviderAccess) []routing.ProviderAccess {
	if len(access) == 0 {
		return nil
	}
	result := make([]routing.ProviderAccess, len(access))
	for i, entry := range access {
		result[i] = routing.ProviderAccess{Provider: entry.Provider}
		if len(entry.Models) > 0 {
			result[i].Models = append([]string(nil), entry.Models...)
		}
	}
	return result
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
		providerState := providerStates[provider]
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
			Latency:         providerState.latency,
			Unavailable:     providerState.unavailable,
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

// modelInputModalities projects the input modalities the catalog states for
// one model onto the planner vocabulary. A modality the planner has no name
// for is dropped rather than guessed, because an invented name would reject
// every request that carries it.
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

// planningModalities carries the request modalities the proxy derived onto
// the planning request. The names cross the boundary as strings, the same
// way required capabilities do.
func planningModalities(names []string) []routing.Modality {
	if len(names) == 0 {
		return nil
	}
	modalities := make([]routing.Modality, 0, len(names))
	for _, name := range names {
		modalities = append(modalities, routing.Modality(name))
	}
	return modalities
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
