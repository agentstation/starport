// Package router provides model routing and fallback capabilities for the Starport gateway.
// It implements OpenRouter-compatible routing with provider preferences, health tracking,
// latency-based routing, cost optimization, and sticky sessions for conversation continuity.
package router

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/availability"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

const (
	// AutoModelID is the special model ID for automatic routing
	AutoModelID = "openrouter/auto"
)

var (
	// ErrNoModelsAvailable is returned when no models can be used
	ErrNoModelsAvailable = errors.New("no models available for routing")
	// ErrAllModelsFailed is returned when all models in the chain failed
	ErrAllModelsFailed = errors.New("all models failed")
)

// Config contains configuration for the router
type Config struct {
	// Latency tracking configuration
	LatencyAlpha      float64 // EMA smoothing factor (0-1)
	LatencyWindowSize int     // Samples before EMA kicks in

	// Cost optimization
	EnableCostOptimization bool

	// Sticky sessions
	EnableStickySessions bool
	SessionTTL           time.Duration

	// Execution owns the one total retry and fallback budget.
	Execution execution.Config

	// Availability owns exact offering circuit state.
	Availability availability.Config
}

// modelRouter implements the ModelRouter interface
type modelRouter struct {
	registry       connectors.Registry
	catalog        *runtimecatalog.ControlPlane
	routePlanner   routing.Planner
	availability   *availability.Tracker
	executor       *execution.Executor
	outcomes       execution.OutcomePublisher
	credentialGate OperatorCredentialGate
	userKeys       UserCredentialResolver

	// Advanced routing features
	config                       Config
	latencyTracker               LatencyTracker
	stickyProviderSessionManager StickyProviderSessionManager
}

// OperatorCredentialGate admits one exact resolved operator material version.
type OperatorCredentialGate interface {
	OperatorMaterialReady(providerID string, materialVersion string) bool
}

// Option configures the transitional router composition.
type Option func(*modelRouter)

// WithCatalog supplies the shared generation-consistent routable snapshot.
func WithCatalog(catalogPlane *runtimecatalog.ControlPlane) Option {
	return func(r *modelRouter) {
		r.catalog = catalogPlane
	}
}

// WithAvailability supplies the one runtime offering availability owner.
func WithAvailability(tracker *availability.Tracker) Option {
	return func(r *modelRouter) {
		r.availability = tracker
	}
}

// WithExecutionConfig replaces the total attempt budget.
func WithExecutionConfig(config execution.Config) Option {
	return func(r *modelRouter) {
		r.config.Execution = config
	}
}

// WithUserCredentials supplies the tenant-scoped inference credential plane.
func WithUserCredentials(resolver UserCredentialResolver) Option {
	return func(r *modelRouter) {
		r.userKeys = resolver
	}
}

// WithOutcomePublisher supplies the safe provider invocation outcome sink.
func WithOutcomePublisher(publisher execution.OutcomePublisher) Option {
	return func(r *modelRouter) {
		r.outcomes = publisher
	}
}

// WithOperatorCredentialGate prevents a provider-proved bad material version
// from being retried before its lifecycle owner supplies a replacement.
func WithOperatorCredentialGate(gate OperatorCredentialGate) Option {
	return func(r *modelRouter) {
		r.credentialGate = gate
	}
}

// New creates a new model router with all features enabled by default
func New(registry connectors.Registry, opts ...Option) ModelRouter {
	config := Config{
		LatencyAlpha:           0.2,
		LatencyWindowSize:      5,
		EnableCostOptimization: true,
		EnableStickySessions:   true,
		SessionTTL:             30 * time.Minute,
		Execution:              execution.DefaultConfig(),
		Availability:           availability.DefaultConfig(),
	}

	latencyTracker := NewLatencyTracker(config.LatencyAlpha, config.LatencyWindowSize)
	router := &modelRouter{
		registry:                     registry,
		routePlanner:                 routing.NewPlanner(),
		latencyTracker:               latencyTracker,
		config:                       config,
		stickyProviderSessionManager: NewStickyProviderSessionManager(config.SessionTTL),
	}
	for _, opt := range opts {
		opt(router)
	}
	if router.availability == nil {
		tracker, err := availability.New(router.config.Availability, nil, router.catalog)
		if err != nil {
			panic(fmt.Sprintf("router: create availability owner: %v", err))
		}
		router.availability = tracker
	}
	executor, err := execution.New(
		router.config.Execution,
		nil,
		router.availability,
		router.outcomes,
	)
	if err != nil {
		panic(fmt.Sprintf("router: create attempt executor: %v", err))
	}
	router.executor = executor

	return router
}

// SelectModel chooses the best model based on the request and routing strategy
func (r *modelRouter) SelectModel(ctx context.Context, req *Request) (string, connectors.Connector, error) {
	runtime, owned, err := r.acquireRuntime(ctx)
	if err != nil {
		return "", nil, ErrNoModelsAvailable
	}
	if owned {
		defer runtime.Release()
	}
	plan, err := r.planRoute(ctx, req, runtime)
	if err != nil {
		if errors.Is(err, routing.ErrNoCandidate) {
			return "", nil, ErrNoModelsAvailable
		}
		return "", nil, fmt.Errorf("plan model route: %w", err)
	}
	attempts := plan.Attempts()
	if len(attempts) == 0 {
		return "", nil, ErrNoModelsAvailable
	}
	selected := attempts[0].Route
	connector := runtime.Get(selected.ProviderID)
	if connector == nil {
		return "", nil, fmt.Errorf("no connector for provider %s", selected.ProviderID)
	}
	return selected.ID(), connector, nil
}

// RouteWithFallback attempts to route a request through multiple models with fallback logic
func (r *modelRouter) RouteWithFallback(ctx context.Context, req *Request) (*Response, error) {
	if req == nil || req.ChatRequest == nil {
		return nil, ErrNoModelsAvailable
	}
	runtime, owned, err := r.acquireRuntime(ctx)
	if err != nil {
		return nil, ErrNoModelsAvailable
	}
	if owned {
		defer runtime.Release()
	}
	plan, err := r.planRoute(ctx, req, runtime)
	if err != nil {
		if errors.Is(err, routing.ErrNoCandidate) {
			return nil, ErrNoModelsAvailable
		}
		return nil, fmt.Errorf("plan fallback route: %w", err)
	}
	strategy, tenantID := credentialRequestPolicy(req)
	credentialPolicy, err := newCredentialPolicy(
		strategy, tenantID, runtime, r.userKeys, r.credentialGate,
	)
	if err != nil {
		return nil, err
	}

	result, err := r.executor.ExecuteChat(ctx, plan, func(attemptCtx context.Context, planned routing.Attempt) (*inference.ChatResponse, *failure.Failure, execution.AttemptAction) {
		connector := runtime.Get(planned.Route.ProviderID)
		if connector == nil {
			return nil, failure.New(
				failure.ProviderUnavailable,
				"No provider adapter is available.",
				true,
				failure.ProviderDetails{Provider: planned.Route.ProviderID},
				nil,
			), execution.AttemptActionDefault
		}
		selected, materialFailure, action := credentialPolicy.resolve(attemptCtx, planned.Route)
		if materialFailure != nil {
			return nil, materialFailure, action
		}
		boundRoute, bindFailure := bindSelectedEndpoint(runtime, planned.Route, selected)
		if bindFailure != nil {
			return nil, bindFailure, execution.AttemptActionStop
		}
		request := prepareChatAttempt(req, boundRoute, false)
		request.Credential = selected.material
		response, requestErr := connector.Chat(attemptCtx, request)
		if requestErr != nil {
			providerFailure := connectors.NormalizeFailure(planned.Route.ProviderID, requestErr)
			return nil, providerFailure, credentialPolicy.afterFailure(planned.Route, providerFailure)
		}
		execution.RecordCredentialAccepted(attemptCtx)
		canonical, conversionErr := connectors.ChatResponseToInference(response, planned.Route.ID())
		if conversionErr != nil {
			return nil, failure.New(
				failure.Internal,
				"The provider response was invalid.",
				false,
				failure.ProviderDetails{Provider: planned.Route.ProviderID},
				conversionErr,
			), execution.AttemptActionDefault
		}
		return &canonical, nil, execution.AttemptActionDefault
	})
	if err != nil {
		evidence := executionEvidence(err)
		return &Response{Metadata: responseMetadata(evidence, "all models failed")}, fmt.Errorf("%w: %w", ErrAllModelsFailed, err)
	}

	wireResponse, err := connectors.ChatResponseFromInference(result.Response)
	if err != nil {
		return nil, fmt.Errorf("convert canonical chat response: %w", err)
	}
	provider := result.Route.ProviderID
	if r.latencyTracker != nil {
		r.latencyTracker.RecordLatency(provider, result.FinishedAt.Sub(result.StartedAt))
	}
	if r.stickyProviderSessionManager != nil && r.config.EnableStickySessions && req.Metadata != nil && req.Metadata.ConversationID != "" {
		r.stickyProviderSessionManager.SetProvider(req.Metadata.ConversationID, provider)
	}

	return &Response{
		ChatResponse:    wireResponse,
		ModelUsed:       result.Route.ID(),
		ProviderUsed:    provider,
		Attempts:        len(result.Attempts),
		Metadata:        responseMetadata(result.Attempts, selectionReason(result.Attempts)),
		CatalogSnapshot: runtime.Snapshot(),
	}, nil
}

// getCandidateModels returns the list of models to try
func (r *modelRouter) getCandidateModels(req *Request) []string {
	if req == nil {
		return nil
	}

	// If Models array is provided in the routing request, use it
	if len(req.Models) > 0 {
		return append([]string(nil), req.Models...)
	}

	// If Models array is provided in the chat request, use it
	if req.ChatRequest != nil && len(req.ChatRequest.Models) > 0 {
		return append([]string(nil), req.ChatRequest.Models...)
	}

	// Otherwise, use the single model
	if req.ChatRequest != nil && req.Model != "" {
		return []string{req.Model}
	}

	return nil
}

// filterByProviderPreferences applies request provider policy with the same
// semantics as the catalog planner: "only" and "ignore" always compose, and
// "order" without fallbacks keeps only the ordered providers. A model
// survives when its provider passes every constraint; ordered providers
// come first in rank order.
func (r *modelRouter) filterByProviderPreferences(models []string, prefs *ProviderPreferences) []string {
	if prefs == nil {
		return models
	}
	only := providerNameSet(prefs.Only)
	ignore := providerNameSet(prefs.Ignore)
	orderRanks := make(map[string]int, len(prefs.Order))
	for index, provider := range prefs.Order {
		name := normalizeProviderName(provider)
		if _, exists := orderRanks[name]; !exists {
			orderRanks[name] = index
		}
	}

	type rankedModel struct {
		model string
		rank  int
	}
	ordered := make([]rankedModel, 0, len(models))
	remaining := make([]string, 0, len(models))
	for _, model := range models {
		provider := normalizeProviderName(r.extractProvider(model))
		if len(only) > 0 {
			if _, allowed := only[provider]; !allowed {
				continue
			}
		}
		if _, ignored := ignore[provider]; ignored {
			continue
		}
		if rank, exists := orderRanks[provider]; exists {
			ordered = append(ordered, rankedModel{model: model, rank: rank})
			continue
		}
		if len(orderRanks) == 0 || prefs.AllowFallbacks {
			remaining = append(remaining, model)
		}
	}
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].rank < ordered[b].rank })

	filtered := make([]string, 0, len(ordered)+len(remaining))
	for _, ranked := range ordered {
		filtered = append(filtered, ranked.model)
	}
	return append(filtered, remaining...)
}

func providerNameSet(providers []string) map[string]struct{} {
	if len(providers) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		set[normalizeProviderName(provider)] = struct{}{}
	}
	return set
}

func normalizeProviderName(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// filterByAPIKeyRestrictions applies API key restrictions
func (r *modelRouter) filterByAPIKeyRestrictions(models []string, config *APIKeyConfig) []string {
	if config == nil || (len(config.AllowedProviders) == 0 && len(config.AllowedModels) == 0) {
		return models
	}

	allowedMap := make(map[string]bool)
	for _, p := range config.AllowedProviders {
		allowedMap[p] = true
	}

	allowedModels := make(map[string]bool)
	allowAllModels := false
	for _, model := range config.AllowedModels {
		if model == "*" {
			allowAllModels = true
			continue
		}
		allowedModels[model] = true
	}

	var filtered []string
	for _, model := range models {
		provider := r.extractProvider(model)
		if len(allowedMap) > 0 && !allowedMap[provider] {
			continue
		}
		if len(allowedModels) > 0 && !allowAllModels && !modelAllowed(model, allowedModels) {
			continue
		}

		// Check for model overrides
		if override, ok := config.ModelOverrides[model]; ok {
			filtered = append(filtered, override)
		} else {
			filtered = append(filtered, model)
		}
	}

	return filtered
}

func modelAllowed(model string, allowedModels map[string]bool) bool {
	if allowedModels[model] {
		return true
	}

	_, modelName, ok := runtimecatalog.SplitModelID(model)
	return ok && allowedModels[modelName]
}

// extractProvider extracts the provider from a model ID
func (r *modelRouter) extractProvider(modelID string) string {
	if route, ok := r.resolveRoute(modelID); ok {
		return string(route.ProviderID)
	}
	// Use the catalog to determine the actual provider
	provider := runtimecatalog.ProviderFromModelID(modelID)
	if provider != "" {
		return provider
	}

	// Fall back to extracting from model ID
	provider, _, ok := runtimecatalog.SplitModelID(modelID)
	if ok {
		return provider
	}
	return ""
}

func (r *modelRouter) resolveRoute(modelID string) (runtimecatalog.Route, bool) {
	if r.catalog == nil {
		return runtimecatalog.Route{}, false
	}
	snapshot := r.catalog.Current()
	if snapshot == nil {
		return runtimecatalog.Route{}, false
	}
	return snapshot.ResolveRoute(modelID)
}
