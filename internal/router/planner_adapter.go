package router

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

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
	if refusal := snapshot.CheckNewAttempt(); refusal != nil {
		return nil, refusal
	}
	var err error
	request.Models, err = resolveModelAliases(snapshot, request.Models)
	if err != nil {
		return nil, err
	}
	request.ZeroPriceModels, err = resolveModelAliases(snapshot, request.ZeroPriceModels)
	if err != nil {
		return nil, err
	}
	request.Operation = operation
	request.Models, request.AllowAnyModelFallback = splitAutoModel(request.Models)
	request.AllowModelFallbacks = len(request.Models) > 1
	input := routing.Snapshot{
		CatalogGenerationID:  snapshot.GenerationID(),
		AvailabilityRevision: snapshot.AvailabilityRevision(),
		Candidates:           r.toPlanningCandidates(snapshot, runtime, request),
	}
	plan, err := r.routePlanner.Plan(request, input)
	if errors.Is(err, routing.ErrNoCandidate) && !request.AllowAnyModelFallback && len(request.Models) > 0 {
		found := false
		for _, name := range request.Models {
			if override := request.Account.ModelOverrides[name]; override != "" {
				name = override
			}
			if catalogNamePermitted(snapshot, request.Account, name) {
				found = true
				break
			}
		}
		if !found {
			return nil, runtimecatalog.ErrModelNotCatalogued
		}
	}
	return plan, err
}

func resolveModelAliases(snapshot *runtimecatalog.RoutableSnapshot, names []string) ([]string, error) {
	result := names
	copied := false
	for index, name := range names {
		resolved, valid := snapshot.ResolveAlias(name)
		if !valid {
			return nil, runtimecatalog.ErrModelNotCatalogued
		}
		if resolved == name {
			continue
		}
		if !copied {
			result = slices.Clone(names)
			copied = true
		}
		result[index] = resolved
	}
	return result, nil
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
	request routing.Request,
) []routing.Candidate {
	names := slices.Clone(request.Models)
	for i, name := range names {
		if override := request.Account.ModelOverrides[name]; override != "" {
			names[i] = override
		}
	}
	candidates := snapshot.PlanningCandidates(names, request.AllowAnyModelFallback || len(names) == 0)
	providerStates := make(map[string]providerPlanningState)
	for i := range candidates {
		provider := candidates[i].Route.ProviderID
		state, exists := providerStates[provider]
		if !exists {
			state = providerPlanningState{
				latency:     measuredProviderLatency(r.latencyTracker, provider),
				unavailable: runtime == nil || runtime.Get(provider) == nil,
			}
			providerStates[provider] = state
		}
		candidates[i].Latency = state.latency
		candidates[i].Unavailable = state.unavailable
	}
	return candidates
}

type providerPlanningState struct {
	latency     *time.Duration
	unavailable bool
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
