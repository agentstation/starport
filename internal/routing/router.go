package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/connectors"
	"github.com/rs/zerolog/log"
)

const (
	// AutoModelID is the special model ID for automatic routing
	AutoModelID = "openrouter/auto"

	// Default retry settings
	defaultMaxRetries       = 3
	defaultRetryDelay       = 1 * time.Second
	defaultBackoffMultiplier = 2.0
)

var (
	// ErrNoModelsAvailable is returned when no models can be used
	ErrNoModelsAvailable = errors.New("no models available for routing")
	// ErrAllModelsFailed is returned when all models in the chain failed
	ErrAllModelsFailed = errors.New("all models failed")
)

// defaultRouter implements the ModelRouter interface
type defaultRouter struct {
	registry         connectors.Registry
	modelSelector    ModelSelector
	availableModels  map[string]ModelInfo
	providerHealthMu sync.RWMutex
	providerHealth   map[string]*ProviderHealth
}

// NewRouter creates a new model router
func NewRouter(registry connectors.Registry) ModelRouter {
	return &defaultRouter{
		registry:        registry,
		modelSelector:   NewDefaultModelSelector(),
		availableModels: make(map[string]ModelInfo),
		providerHealth:  make(map[string]*ProviderHealth),
	}
}

// ProviderHealth tracks provider health status
type ProviderHealth struct {
	Available       bool
	LastError       error
	LastErrorTime   time.Time
	ConsecutiveFails int
	CircuitOpen     bool
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
func (r *defaultRouter) SelectModel(ctx context.Context, req *Request) (string, connectors.Connector, error) {
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
func (r *defaultRouter) RouteWithFallback(ctx context.Context, req *Request) (*Response, error) {
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
func (r *defaultRouter) getCandidateModels(req *Request) []string {
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
func (r *defaultRouter) expandAutoModel(models []string, req *Request) []string {
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
func (r *defaultRouter) filterByProviderPreferences(models []string, prefs *ProviderPreferences) []string {
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
func (r *defaultRouter) filterByAPIKeyRestrictions(models []string, config *APIKeyConfig) []string {
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
func (r *defaultRouter) selectBestModel(_ context.Context, models []string, _ *Request) string {
	// For now, just return the first model
	// In a full implementation, this would consider:
	// - Model capabilities vs requirements
	// - Provider latency (EMA)
	// - Cost optimization
	// - Context length requirements
	if len(models) > 0 {
		return models[0]
	}
	return ""
}

// extractProvider extracts the provider from a model ID
func (r *defaultRouter) extractProvider(modelID string) string {
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// isProviderHealthy checks if a provider is healthy
func (r *defaultRouter) isProviderHealthy(provider string) bool {
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
func (r *defaultRouter) recordProviderSuccess(provider string) {
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
func (r *defaultRouter) recordProviderFailure(provider string, err error) {
	r.providerHealthMu.Lock()
	defer r.providerHealthMu.Unlock()

	health, exists := r.providerHealth[provider]
	if !exists {
		health = &ProviderHealth{}
		r.providerHealth[provider] = health
	}

	health.LastError = err
	health.LastErrorTime = time.Now()
	health.ConsecutiveFails++

	// Open circuit if too many consecutive failures
	if health.ConsecutiveFails >= 3 {
		health.CircuitOpen = true
		health.CircuitOpenUntil = time.Now().Add(30 * time.Second)
		health.Available = false
	}
}

// attemptStreamingRequest handles streaming requests with fallback
func (r *defaultRouter) attemptStreamingRequest(_ context.Context, _ connectors.Connector, _ *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	// For now, return an error - streaming fallback is complex
	// In production, we'd need to handle stream interruption and retry
	return nil, fmt.Errorf("streaming fallback not yet implemented")
}

// delayBeforeRetry adds a delay before retrying with exponential backoff
func (r *defaultRouter) delayBeforeRetry(ctx context.Context, attemptNum int) {
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
func (r *defaultRouter) getSelectionReason(index, total int) string {
	if index == 0 {
		return "primary model succeeded"
	}
	return fmt.Sprintf("fallback model %d of %d succeeded", index+1, total)
}

// triggerToString converts a fallback trigger to a string
func (r *defaultRouter) triggerToString(trigger FallbackTrigger) string {
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