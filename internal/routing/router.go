// Package routing provides model routing and fallback capabilities for the Starport gateway.
// It implements OpenRouter-compatible routing with provider preferences, health tracking,
// latency-based routing, cost optimization, and sticky sessions for conversation continuity.
package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/pkg/catalog"
	"github.com/rs/zerolog/log"
)

const (
	// AutoModelID is the special model ID for automatic routing
	AutoModelID = "openrouter/auto"

	// Default retry settings
	defaultMaxRetries        = 3
	defaultRetryDelay        = 1 * time.Second
	defaultBackoffMultiplier = 2.0
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
	MaxCostMultiplier      float64

	// Latency constraints
	MaxLatencyMultiplier float64

	// Sticky sessions
	EnableStickySessions bool
	SessionTTL           time.Duration

	// Health check configuration
	CircuitBreakerThreshold int           // Consecutive failures to open circuit
	CircuitBreakerDuration  time.Duration // How long to keep circuit open

	// Retry configuration
	MaxRetries             int
	RetryDelay             time.Duration
	RetryBackoffMultiplier float64
}

// modelRouter implements the ModelRouter interface
type modelRouter struct {
	registry         connectors.Registry
	modelSelector    ModelSelector
	availableModels  map[string]ModelInfo
	providerHealthMu sync.RWMutex
	providerHealth   map[string]*ProviderHealth

	// Advanced routing features
	config                       Config
	latencyTracker               LatencyTracker
	costCalculator               CostCalculator
	stickyProviderSessionManager StickyProviderSessionManager
	latencySelector              *LatencyBasedSelector
	costSelector                 *CostOptimizedSelector
}

// NewRouter creates a new model router with all features enabled by default
func NewRouter(registry connectors.Registry) ModelRouter {
	config := Config{
		LatencyAlpha:            0.2,
		LatencyWindowSize:       5,
		EnableCostOptimization:  true,
		MaxCostMultiplier:       2.0,
		MaxLatencyMultiplier:    2.0,
		EnableStickySessions:    true,
		SessionTTL:              30 * time.Minute,
		CircuitBreakerThreshold: 3,
		CircuitBreakerDuration:  30 * time.Second,
		MaxRetries:              3,
		RetryDelay:              1 * time.Second,
		RetryBackoffMultiplier:  2.0,
	}

	latencyTracker := NewLatencyTracker(config.LatencyAlpha, config.LatencyWindowSize)
	costCalculator := NewCostCalculator()

	router := &modelRouter{
		registry:                     registry,
		modelSelector:                NewDefaultModelSelector(),
		availableModels:              make(map[string]ModelInfo),
		providerHealth:               make(map[string]*ProviderHealth),
		latencyTracker:               latencyTracker,
		costCalculator:               costCalculator,
		config:                       config,
		latencySelector:              NewLatencyBasedSelector(latencyTracker, config.MaxLatencyMultiplier),
		costSelector:                 NewCostOptimizedSelector(costCalculator, latencyTracker, config.MaxCostMultiplier, config.MaxLatencyMultiplier),
		stickyProviderSessionManager: NewStickyProviderSessionManager(config.SessionTTL),
	}

	return router
}

// ProviderHealth tracks provider health status
type ProviderHealth struct {
	Available        bool
	LastError        error
	LastErrorTime    time.Time
	ConsecutiveFails int
	CircuitOpen      bool
	CircuitOpenUntil time.Time
}

// ModelInfo contains information about a model
type ModelInfo struct {
	ID              string
	Provider        string
	ContextLength   int
	MaxOutputTokens int
	Features        []string
	Available       bool
	LastChecked     time.Time
}

// SelectModel chooses the best model based on the request and routing strategy
func (r *modelRouter) SelectModel(ctx context.Context, req *Request) (string, connectors.Connector, error) {
	// Check sticky session first
	if r.stickyProviderSessionManager != nil && r.config.EnableStickySessions && req.Metadata != nil && req.Metadata.ConversationID != "" {
		if provider, exists := r.stickyProviderSessionManager.GetProvider(req.Metadata.ConversationID); exists {
			// Check if provider is still healthy
			if r.isProviderHealthy(provider) {
				// Find a model from this provider in our candidates
				models := r.getCandidateModels(req)
				for _, model := range models {
					if r.extractProvider(model) == provider {
						connector := r.registry.Get(provider)
						if connector != nil {
							return model, connector, nil
						}
					}
				}
			}
		}
	}

	// Get candidate models
	models := r.getCandidateModels(req)
	if len(models) == 0 {
		return "", nil, ErrNoModelsAvailable
	}

	// Filter by provider preferences
	models = r.filterByProviderPreferences(models, req.ProviderPreferences)
	if len(models) == 0 {
		return "", nil, fmt.Errorf("no models match provider preferences")
	}

	// Filter by API key restrictions
	if req.APIKeyConfig != nil {
		models = r.filterByAPIKeyRestrictions(models, req.APIKeyConfig)
		if len(models) == 0 {
			return "", nil, fmt.Errorf("no models allowed for this API key")
		}
	}

	// Filter by health
	models = r.filterByHealth(models)
	if len(models) == 0 {
		return "", nil, ErrNoModelsAvailable
	}

	// Apply latency-based filtering if available
	if r.latencySelector != nil {
		providers := r.extractProviders(models)
		filteredProviders := r.latencySelector.FilterByLatency(providers)
		models = r.filterModelsByProviders(models, filteredProviders)
	}

	// Select the best model
	modelID := r.selectBestModel(ctx, models, req)

	// Get connector for the model
	provider := r.extractProvider(modelID)
	connector := r.registry.Get(provider)
	if connector == nil {
		return "", nil, fmt.Errorf("no connector for provider %s", provider)
	}

	return modelID, connector, nil
}

// RouteWithFallback attempts to route a request through multiple models with fallback logic
func (r *modelRouter) RouteWithFallback(ctx context.Context, req *Request) (*Response, error) {
	startTime := time.Now()

	// Get candidate models
	models := r.getCandidateModels(req)
	if len(models) == 0 {
		return nil, ErrNoModelsAvailable
	}

	// Apply filters
	models = r.filterByProviderPreferences(models, req.ProviderPreferences)
	if req.APIKeyConfig != nil {
		models = r.filterByAPIKeyRestrictions(models, req.APIKeyConfig)
	}

	if len(models) == 0 {
		return nil, ErrNoModelsAvailable
	}

	var attempts []ModelAttempt
	var lastError error

	// Try each model in sequence
	for i, modelID := range models {
		attemptStart := time.Now()
		provider := r.extractProvider(modelID)

		// Check provider health
		if !r.isProviderHealthy(provider) {
			attempts = append(attempts, ModelAttempt{
				Model:    modelID,
				Provider: provider,
				Status:   "skipped",
				Error:    "provider circuit open",
				Duration: time.Since(attemptStart),
			})
			continue
		}

		// Get connector
		connector := r.registry.Get(provider)
		if connector == nil {
			attempts = append(attempts, ModelAttempt{
				Model:    modelID,
				Provider: provider,
				Status:   "failed",
				Error:    "no connector available",
				Duration: time.Since(attemptStart),
			})
			continue
		}

		// Create a copy of the request with the current model
		reqCopy := *req.ChatRequest
		reqCopy.Model = modelID

		// Attempt the request
		var resp *connectors.ChatResponse
		var err error

		if reqCopy.Stream {
			// For streaming, we need to handle it differently
			// This is a simplified version - in production, we'd need to handle streaming fallback
			resp, err = r.attemptStreamingRequest(ctx, connector, &reqCopy)
		} else {
			resp, err = connector.Chat(ctx, &reqCopy)
		}

		if err == nil {
			// Success!
			r.recordProviderSuccess(provider)

			// Record latency if tracker is available
			if r.latencyTracker != nil {
				latency := time.Since(attemptStart)
				r.latencyTracker.RecordLatency(provider, latency)
			}

			// Update sticky provider session if enabled
			if r.stickyProviderSessionManager != nil && r.config.EnableStickySessions && req.Metadata != nil && req.Metadata.ConversationID != "" {
				r.stickyProviderSessionManager.SetProvider(req.Metadata.ConversationID, provider)
			}

			return &Response{
				ChatResponse: resp,
				ModelUsed:    modelID,
				ProviderUsed: provider,
				Attempts:     len(attempts) + 1,
				Metadata: &Metadata{
					ModelsAttempted: append(attempts, ModelAttempt{
						Model:    modelID,
						Provider: provider,
						Status:   "success",
						Duration: time.Since(attemptStart),
					}),
					RoutingDuration: time.Since(startTime),
					SelectionReason: r.getSelectionReason(i, len(models)),
				},
			}, nil
		}

		// Record the failure
		r.recordProviderFailure(provider, err)
		lastError = err

		// Check if we should fallback
		trigger, shouldFallback := IsFallbackError(err)
		errorMsg := err.Error()

		attempts = append(attempts, ModelAttempt{
			Model:    modelID,
			Provider: provider,
			Status:   "failed",
			Error:    errorMsg,
			Duration: time.Since(attemptStart),
		})

		if !shouldFallback {
			// This error shouldn't trigger fallback
			break
		}

		// Log the fallback
		log.Ctx(ctx).Warn().
			Str("model", modelID).
			Str("provider", provider).
			Str("trigger", r.triggerToString(trigger)).
			Err(err).
			Msg("falling back to next model")

		// Add delay before retry (except for last attempt)
		if i < len(models)-1 {
			r.delayBeforeRetry(ctx, i)
		}
	}

	// All models failed
	return &Response{
		Metadata: &Metadata{
			ModelsAttempted: attempts,
			RoutingDuration: time.Since(startTime),
			SelectionReason: "all models failed",
		},
	}, fmt.Errorf("%w: %v", ErrAllModelsFailed, lastError)
}

// getCandidateModels returns the list of models to try
func (r *modelRouter) getCandidateModels(req *Request) []string {
	// If Models array is provided in the routing request, use it
	if len(req.Models) > 0 {
		return r.expandAutoModel(req.Models, req)
	}

	// If Models array is provided in the chat request, use it
	if req.ChatRequest != nil && len(req.ChatRequest.Models) > 0 {
		return r.expandAutoModel(req.ChatRequest.Models, req)
	}

	// Otherwise, use the single model
	if req.ChatRequest != nil && req.Model != "" {
		models := []string{req.Model}
		return r.expandAutoModel(models, req)
	}

	return nil
}

// expandAutoModel expands "openrouter/auto" to actual models
func (r *modelRouter) expandAutoModel(models []string, req *Request) []string {
	var expanded []string

	for _, model := range models {
		if model == AutoModelID {
			// Get auto-selected models
			autoModels := r.modelSelector.SelectModels(req)
			expanded = append(expanded, autoModels...)
		} else {
			expanded = append(expanded, model)
		}
	}

	return expanded
}

// filterByProviderPreferences applies provider routing preferences
func (r *modelRouter) filterByProviderPreferences(models []string, prefs *ProviderPreferences) []string {
	if prefs == nil {
		return models
	}

	var filtered []string
	providersSeen := make(map[string]bool)

	// If "only" is specified, only use those providers
	if len(prefs.Only) > 0 {
		onlyMap := make(map[string]bool)
		for _, p := range prefs.Only {
			onlyMap[p] = true
		}

		for _, model := range models {
			provider := r.extractProvider(model)
			if onlyMap[provider] {
				filtered = append(filtered, model)
				providersSeen[provider] = true
			}
		}
		return filtered
	}

	// Build ignore map
	ignoreMap := make(map[string]bool)
	for _, p := range prefs.Ignore {
		ignoreMap[p] = true
	}

	// If "order" is specified, sort by that order
	if len(prefs.Order) > 0 {
		// First add models from ordered providers
		for _, preferredProvider := range prefs.Order {
			if ignoreMap[preferredProvider] {
				continue
			}

			for _, model := range models {
				provider := r.extractProvider(model)
				if provider == preferredProvider && !providersSeen[provider] {
					filtered = append(filtered, model)
					providersSeen[provider] = true
				}
			}
		}

		// Then add remaining models if fallbacks are allowed
		if prefs.AllowFallbacks {
			for _, model := range models {
				provider := r.extractProvider(model)
				if !providersSeen[provider] && !ignoreMap[provider] {
					filtered = append(filtered, model)
					providersSeen[provider] = true
				}
			}
		}
	} else {
		// No order specified, just apply ignore list
		for _, model := range models {
			provider := r.extractProvider(model)
			if !ignoreMap[provider] {
				filtered = append(filtered, model)
			}
		}
	}

	return filtered
}

// filterByAPIKeyRestrictions applies API key restrictions
func (r *modelRouter) filterByAPIKeyRestrictions(models []string, config *APIKeyConfig) []string {
	if config == nil || len(config.AllowedProviders) == 0 {
		return models
	}

	allowedMap := make(map[string]bool)
	for _, p := range config.AllowedProviders {
		allowedMap[p] = true
	}

	var filtered []string
	for _, model := range models {
		provider := r.extractProvider(model)
		if allowedMap[provider] {
			// Check for model overrides
			if override, ok := config.ModelOverrides[model]; ok {
				filtered = append(filtered, override)
			} else {
				filtered = append(filtered, model)
			}
		}
	}

	return filtered
}

// selectBestModel selects the best model from candidates
func (r *modelRouter) selectBestModel(_ context.Context, models []string, req *Request) string {
	if len(models) == 0 {
		return ""
	}

	// Use cost optimization if enabled and we have token estimates
	if r.costSelector != nil && r.config.EnableCostOptimization && req.Metadata != nil && req.Metadata.EstimatedTokens > 0 {
		selectedModel := r.costSelector.SelectModel(
			models,
			req.Metadata.EstimatedTokens,
			req.Metadata.EstimatedTokens/4, // Rough estimate for completion
		)
		if selectedModel != "" {
			return selectedModel
		}
	}

	// Otherwise use latency-based selection if available
	if r.latencySelector != nil {
		providers := r.extractProviders(models)
		bestProvider := r.latencySelector.SelectProvider(providers)
		for _, model := range models {
			if r.extractProvider(model) == bestProvider {
				return model
			}
		}
	}

	// Fallback to first model
	return models[0]
}

// extractProvider extracts the provider from a model ID
func (r *modelRouter) extractProvider(modelID string) string {
	// Use the catalog to determine the actual provider
	provider := catalog.GetProviderForModel(modelID)
	if provider != "" {
		return provider
	}
	
	// Fall back to extracting from model ID
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// isProviderHealthy checks if a provider is healthy
func (r *modelRouter) isProviderHealthy(provider string) bool {
	r.providerHealthMu.RLock()
	health, exists := r.providerHealth[provider]
	r.providerHealthMu.RUnlock()

	if !exists {
		// No health info, assume healthy
		return true
	}

	// Check circuit breaker
	if health.CircuitOpen && time.Now().Before(health.CircuitOpenUntil) {
		return false
	}

	return health.Available
}

// recordProviderSuccess records a successful request
func (r *modelRouter) recordProviderSuccess(provider string) {
	r.providerHealthMu.Lock()
	defer r.providerHealthMu.Unlock()

	if health, exists := r.providerHealth[provider]; exists {
		health.ConsecutiveFails = 0
		health.CircuitOpen = false
		health.Available = true
	} else {
		r.providerHealth[provider] = &ProviderHealth{
			Available: true,
		}
	}
}

// recordProviderFailure records a failed request
func (r *modelRouter) recordProviderFailure(provider string, err error) {
	r.providerHealthMu.Lock()
	defer r.providerHealthMu.Unlock()

	health, exists := r.providerHealth[provider]
	if !exists {
		health = &ProviderHealth{
			Available: true, // Default to available
		}
		r.providerHealth[provider] = health
	}

	health.LastError = err
	health.LastErrorTime = time.Now()
	health.ConsecutiveFails++

	// Open circuit if too many consecutive failures
	threshold := r.config.CircuitBreakerThreshold
	if threshold == 0 {
		threshold = 3 // Default
	}
	duration := r.config.CircuitBreakerDuration
	if duration == 0 {
		duration = 30 * time.Second // Default
	}

	if health.ConsecutiveFails >= threshold {
		health.CircuitOpen = true
		health.CircuitOpenUntil = time.Now().Add(duration)
		health.Available = false
	}
}

// attemptStreamingRequest handles streaming requests with fallback
func (r *modelRouter) attemptStreamingRequest(_ context.Context, _ connectors.Connector, _ *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	// For now, return an error - streaming fallback is complex
	// In production, we'd need to handle stream interruption and retry
	return nil, fmt.Errorf("streaming fallback not yet implemented")
}

// delayBeforeRetry adds a delay before retrying with exponential backoff
func (r *modelRouter) delayBeforeRetry(ctx context.Context, attemptNum int) {
	// Ensure attemptNum is within safe bounds to prevent overflow
	if attemptNum > 30 {
		attemptNum = 30 // Cap at 2^30 to prevent overflow
	}
	delay := defaultRetryDelay * time.Duration(1<<uint(attemptNum)) // #nosec G115 - attemptNum is bounded above
	if delay > 10*time.Second {
		delay = 10 * time.Second
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
		return
	}
}

// getSelectionReason returns a human-readable reason for model selection
func (r *modelRouter) getSelectionReason(index, total int) string {
	if index == 0 {
		return "primary model succeeded"
	}
	return fmt.Sprintf("fallback model %d of %d succeeded", index+1, total)
}

// triggerToString converts a fallback trigger to a string
func (r *modelRouter) triggerToString(trigger FallbackTrigger) string {
	switch trigger {
	case FallbackRateLimit:
		return "rate_limit"
	case FallbackModelUnavailable:
		return "model_unavailable"
	case FallbackContextExceeded:
		return "context_exceeded"
	case FallbackProviderError:
		return "provider_error"
	case FallbackContentModeration:
		return "content_moderation"
	case FallbackTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// filterByHealth filters out unhealthy providers
func (r *modelRouter) filterByHealth(models []string) []string {
	var healthy []string
	for _, model := range models {
		provider := r.extractProvider(model)
		if r.isProviderHealthy(provider) {
			healthy = append(healthy, model)
		}
	}
	return healthy
}

// extractProviders gets unique providers from model list
func (r *modelRouter) extractProviders(models []string) []string {
	seen := make(map[string]bool)
	var providers []string

	for _, model := range models {
		provider := r.extractProvider(model)
		if !seen[provider] {
			seen[provider] = true
			providers = append(providers, provider)
		}
	}

	return providers
}

// filterModelsByProviders filters models to only include those from specified providers
func (r *modelRouter) filterModelsByProviders(models []string, providers []string) []string {
	providerMap := make(map[string]bool)
	for _, p := range providers {
		providerMap[p] = true
	}

	var filtered []string
	for _, model := range models {
		if providerMap[r.extractProvider(model)] {
			filtered = append(filtered, model)
		}
	}

	return filtered
}
