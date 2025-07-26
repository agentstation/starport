// Package proxy provides a high-performance LLM request proxy with support for
// multiple providers, intelligent routing, caching, and extensible middleware.
package proxy

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/pkg/catalog"
)

// Config holds the configuration for creating a new proxy service.
type Config struct {
	// Registry provides access to LLM provider connectors
	Registry *registry.Registry
	
	// Router handles intelligent model selection and failover
	Router routing.ModelRouter
	
	// CacheManager handles response caching (optional)
	CacheManager *cache.Manager
	
	// CacheConfig configures caching behavior (optional)
	CacheConfig *CacheConfig
	
	// Middlewares to apply to the proxy service
	Middlewares []Middleware
}

// Option configures the proxy service.
type Option func(*Config)

// WithCache enables caching with the specified cache manager and configuration.
func WithCache(manager *cache.Manager, config *CacheConfig) Option {
	return func(c *Config) {
		c.CacheManager = manager
		c.CacheConfig = config
	}
}

// WithCacheConfig sets custom cache configuration.
// If a cache manager is not provided separately, a default one will be created.
func WithCacheConfig(config *CacheConfig) Option {
	return func(c *Config) {
		c.CacheConfig = config
	}
}

// WithMiddleware adds a middleware to the proxy service.
// Middlewares are applied in the order they are added.
func WithMiddleware(m Middleware) Option {
	return func(c *Config) {
		c.Middlewares = append(c.Middlewares, m)
	}
}

// New creates a new proxy service with the given registry and router.
// Additional functionality can be added using options.
//
// Example:
//
//	// Basic proxy
//	proxy := proxy.New(registry, router)
//	
//	// Proxy with caching
//	proxy := proxy.New(registry, router,
//	    proxy.WithCache(cacheManager, cacheConfig),
//	)
//	
//	// Proxy with custom middleware
//	proxy := proxy.New(registry, router,
//	    proxy.WithMiddleware(loggingMiddleware),
//	    proxy.WithMiddleware(metricsMiddleware),
//	)
func New(registry *registry.Registry, router routing.ModelRouter, opts ...Option) Proxy {
	// Initialize config with required dependencies
	cfg := &Config{
		Registry: registry,
		Router:   router,
	}
	
	// Apply options
	for _, opt := range opts {
		opt(cfg)
	}
	
	// Create the core proxy implementation
	core := &proxy{
		registry: cfg.Registry,
		router:   cfg.Router,
	}
	
	// Build the proxy with middleware chain
	var p Proxy = core
	
	// Apply custom middlewares in reverse order so the first middleware
	// added is the outermost (called first)
	for i := len(cfg.Middlewares) - 1; i >= 0; i-- {
		p = cfg.Middlewares[i].Wrap(p)
	}
	
	// Add cache middleware if configured
	if cfg.CacheManager != nil && cfg.CacheConfig != nil {
		cacheMiddleware := NewCacheMiddleware(cfg.CacheManager, cfg.CacheConfig)
		p = cacheMiddleware.Wrap(p)
	}
	
	return p
}

// NewFromConfig creates a new proxy service from a configuration struct.
// This is useful when you have a pre-built configuration.
func NewFromConfig(config *Config) Proxy {
	if config.Registry == nil || config.Router == nil {
		panic("proxy: Registry and Router are required")
	}
	
	return New(config.Registry, config.Router,
		WithCache(config.CacheManager, config.CacheConfig),
		func(c *Config) {
			c.Middlewares = config.Middlewares
		},
	)
}

// proxy implements the Proxy interface
type proxy struct {
	registry *registry.Registry
	router   routing.ModelRouter
}

// ProcessChatCompletion handles chat completion requests with routing
func (p *proxy) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Validate request
	if err := ValidateChatCompletionRequest(req); err != nil {
		return nil, err
	}

	// Transform to connector request
	connReq := TransformChatRequest(req)
	
	// Check if request has cache control
	hasCacheControl := false
	for _, msg := range connReq.Messages {
		if connectors.HasCacheControl(msg.Content) {
			hasCacheControl = true
			break
		}
	}

	// Create routing request
	routingReq := &routing.Request{
		ChatRequest: connReq,
		Models:      req.Models,
		// TODO: Add provider preferences and API key config from context
	}

	// Route the request with fallback
	result, err := p.router.RouteWithFallback(ctx, routingReq)
	if err != nil {
		return nil, &RoutingError{
			Model:  req.Model,
			Reason: "failed to route request",
			Err:    err,
		}
	}

	// Extract provider from selected model
	provider := extractProviderFromModelID(result.ModelUsed)

	// If provider doesn't support cache control, strip it from the request
	if hasCacheControl && !ProviderSupportsCacheControl(provider) {
		// Create a copy of the request with cache control stripped
		strippedMessages := make([]connectors.Message, len(connReq.Messages))
		for i, msg := range connReq.Messages {
			strippedContent, err := connectors.StripCacheControl(msg.Content)
			if err != nil {
				// Log error but continue with original content
				strippedContent = msg.Content
			}
			strippedMessages[i] = connectors.Message{
				Role:       msg.Role,
				Content:    strippedContent,
				Reasoning:  msg.Reasoning,
				Name:       msg.Name,
				ToolCalls:  msg.ToolCalls,
				ToolCallID: msg.ToolCallID,
			}
		}
		// Re-execute the request with stripped messages
		strippedReq := &connectors.ChatRequest{
			Model:            result.ModelUsed,
			Messages:         strippedMessages,
			Temperature:      connReq.Temperature,
			TopP:             connReq.TopP,
			MaxTokens:        connReq.MaxTokens,
			Stream:           connReq.Stream,
			Stop:             connReq.Stop,
			PresencePenalty:  connReq.PresencePenalty,
			FrequencyPenalty: connReq.FrequencyPenalty,
			LogitBias:        connReq.LogitBias,
			User:             connReq.User,
			Seed:             connReq.Seed,
			Tools:            connReq.Tools,
			ToolChoice:       connReq.ToolChoice,
			ResponseFormat:   connReq.ResponseFormat,
			Models:           connReq.Models,
			Reasoning:        connReq.Reasoning,
			ProviderOptions:  connReq.ProviderOptions,
		}
		// Get connector from registry
		connector, err := p.registry.Get(provider)
		if err != nil {
			return nil, &ProviderError{
				Provider: provider,
				Code:     "provider_not_found",
				Message:  "provider not available",
				Err:      err,
			}
		}
		// Re-execute with stripped request
		newResp, err := connector.Chat(ctx, strippedReq)
		if err != nil {
			return nil, &ProviderError{
				Provider: provider,
				Code:     "chat_failed",
				Message:  "failed to process chat request",
				Err:      err,
			}
		}
		result.ChatResponse = newResp
	}

	// Transform response
	proxyResp := TransformChatResponse(result.ChatResponse, result.ModelUsed)

	// Add model_used field for OpenRouter compatibility
	proxyResp.ModelUsed = result.ModelUsed
	
	// Calculate cache costs if cache control was used
	if hasCacheControl && result.ChatResponse != nil && result.Usage.PromptTokens > 0 {
		cachePricing := connectors.GetCachePricing(result.ModelUsed)
		if cachePricing != nil {
			// Calculate cache costs
			// For now, assume all cached tokens are writes (conservative estimate)
			// In a real implementation, we'd track cache hits/misses
			promptCost := float64(result.Usage.PromptTokens) / 1000000.0
			writeCost := 0.0
			readCost := 0.0
			
			if cachePricing.CacheWrite != "" {
				writeRate, _ := strconv.ParseFloat(cachePricing.CacheWrite, 64)
				writeCost = promptCost * writeRate
			}
			
			proxyResp.CacheCost = &CacheCost{
				WriteTokens: writeCost,
				ReadTokens:  readCost,
				TotalCost:   writeCost + readCost,
			}
		}
	}

	return proxyResp, nil
}

// ProcessChatCompletionStream handles streaming chat completion requests
func (p *proxy) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	// Validate request
	if err := ValidateChatCompletionRequest(req); err != nil {
		return nil, err
	}

	// Ensure streaming is requested
	if !req.Stream {
		return nil, ErrStreamingNotSupported
	}

	// Transform to connector request
	connReq := TransformChatRequest(req)
	connReq.Stream = true
	
	// Check if request has cache control
	hasCacheControl := false
	for _, msg := range connReq.Messages {
		if connectors.HasCacheControl(msg.Content) {
			hasCacheControl = true
			break
		}
	}

	// Create routing request
	routingReq := &routing.Request{
		ChatRequest: connReq,
		Models:      req.Models,
		// TODO: Add provider preferences and API key config from context
	}

	// Select model using router
	modelID, connector, err := p.router.SelectModel(ctx, routingReq)
	if err != nil {
		return nil, &RoutingError{
			Model:  req.Model,
			Reason: "failed to route request",
			Err:    err,
		}
	}

	// Extract provider from model ID
	provider := extractProviderFromModelID(modelID)

	// Update request with selected model
	connReq.Model = modelID
	
	// If provider doesn't support cache control, strip it from the request
	if hasCacheControl && !ProviderSupportsCacheControl(provider) {
		// Create a copy of the request with cache control stripped
		strippedMessages := make([]connectors.Message, len(connReq.Messages))
		for i, msg := range connReq.Messages {
			strippedContent, err := connectors.StripCacheControl(msg.Content)
			if err != nil {
				// Log error but continue with original content
				strippedContent = msg.Content
			}
			strippedMessages[i] = connectors.Message{
				Role:       msg.Role,
				Content:    strippedContent,
				Reasoning:  msg.Reasoning,
				Name:       msg.Name,
				ToolCalls:  msg.ToolCalls,
				ToolCallID: msg.ToolCallID,
			}
		}
		connReq.Messages = strippedMessages
	}

	// Start streaming
	stream, err := connector.ChatStream(ctx, connReq)
	if err != nil {
		return nil, &ProviderError{
			Provider: provider,
			Code:     "stream_failed",
			Message:  "failed to start stream",
			Err:      err,
		}
	}

	// Wrap the stream to add model_used field
	return NewStreamWrapper(stream, modelID), nil
}

// ProcessEmbeddings handles embedding generation requests
func (p *proxy) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Validate request
	if err := ValidateEmbeddingsRequest(req); err != nil {
		return nil, err
	}

	// Transform to connector request
	connReq := TransformEmbeddingsRequest(req)

	// Extract provider from model if specified
	provider, model := ExtractProviderFromModel(req.Model)

	var connector connectors.Connector
	var err error

	if provider != "" {
		// Direct provider specified
		connector, err = p.registry.Get(provider)
		if err != nil {
			return nil, &ProviderError{
				Provider: provider,
				Code:     "provider_not_found",
				Message:  "provider not available",
				Err:      err,
			}
		}
		connReq.Model = model
	} else {
		// Need to find a provider that supports this model
		// For embeddings, we'll use a simplified approach
		connector, provider, err = p.findEmbeddingsProvider(ctx, req.Model)
		if err != nil {
			return nil, err
		}
	}

	// Execute the request
	resp, err := connector.Embeddings(ctx, connReq)
	if err != nil {
		return nil, &ProviderError{
			Provider: provider,
			Code:     "embeddings_failed",
			Message:  "failed to generate embeddings",
			Err:      err,
		}
	}

	// Transform response
	proxyResp := TransformEmbeddingsResponse(resp)

	return proxyResp, nil
}

// ListModels returns available models based on routing configuration
func (p *proxy) ListModels(ctx context.Context) (*ModelsResponse, error) {
	models, err := p.registry.GetModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get models: %w", err)
	}

	// Transform []connectors.Model to response
	modelInfos := make([]ModelInfo, len(models))
	for i, m := range models {
		modelInfo := ModelInfo{
			ID:      m.ID,
			Object:  "model",
			Created: m.Created,
			OwnedBy: m.OwnedBy,
		}
		
		// Enrich with metadata if available
		metadata := connectors.GetModelMetadata(m.ID)
		if metadata != nil {
			if metadata.Pricing != nil {
				modelInfo.Pricing = &ModelPricing{
					Prompt:     metadata.Pricing.Prompt,
					Completion: metadata.Pricing.Completion,
					Currency:   "USD",
				}
			}
			if metadata.Context != nil {
				// Convert ModelContext (int) to *int
				ctx := int(*metadata.Context)
				modelInfo.Context = &ctx
			}
			if metadata.Description != "" {
				modelInfo.Description = metadata.Description
			}
			// Note: Architecture field not available in ModelInfo struct
			// Would need to extend ModelInfo to include architecture data
		}
		
		modelInfos[i] = modelInfo
	}

	return &ModelsResponse{
		Object: "list",
		Data:   modelInfos,
	}, nil
}

// ListProviders returns available provider information
func (p *proxy) ListProviders(_ context.Context) (*ProvidersResponse, error) {
	metadata := p.registry.GetProviderMetadata()

	// Transform to response
	providerInfos := make([]ProviderInfo, len(metadata))
	for i, m := range metadata {
		providerInfos[i] = ProviderInfo{
			ID:           m.Slug,
			Name:         m.Name,
			Description:  fmt.Sprintf("Provider: %s", m.Name),
			URL:          m.PrivacyPolicyURL,
			RequiresAuth: true,
		}
	}

	return &ProvidersResponse{
		Providers: providerInfos,
	}, nil
}

// GetModelEndpoints returns provider endpoints for a specific model
func (p *proxy) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	// Extract provider from model ID if present
	provider, model := ExtractProviderFromModel(modelID)

	endpoints := []EndpointInfo{}

	if provider != "" {
		// Check if this specific provider supports the model
		if p.registry.HasProvider(provider) {
			connector, _ := p.registry.Get(provider)
			modelsResp, _ := connector.Models(ctx)

			if modelsResp != nil && modelsResp.Data != nil {
				for _, m := range modelsResp.Data {
					if m.ID == modelID || m.ID == model {
						endpoints = append(endpoints, EndpointInfo{
							Provider:  provider,
							Endpoint:  "/api/v1/chat/completions",
							Available: true,
						})
						break
					}
				}
			}
		}
	} else {
		// Check all providers for this model
		for _, prov := range p.registry.ListProviders() {
			connector, _ := p.registry.Get(prov)
			modelsResp, _ := connector.Models(ctx)

			if modelsResp != nil && modelsResp.Data != nil {
				for _, m := range modelsResp.Data {
					// Check if model ID matches (with or without provider prefix)
					if m.ID == modelID || m.ID == fmt.Sprintf("%s/%s", prov, modelID) {
						endpoints = append(endpoints, EndpointInfo{
							Provider:  prov,
							Endpoint:  "/api/v1/chat/completions",
							Available: true,
						})
						break
					}
				}
			}
		}
	}

	return &ModelEndpointsResponse{
		Model:     modelID,
		Endpoints: endpoints,
	}, nil
}

// findEmbeddingsProvider finds a provider that supports embeddings for the given model
func (p *proxy) findEmbeddingsProvider(ctx context.Context, modelID string) (connectors.Connector, string, error) {
	// Check each provider for embeddings support
	for _, provider := range p.registry.ListProviders() {
		connector, _ := p.registry.Get(provider)

		// Check if provider has the model
		modelsResp, err := connector.Models(ctx)
		if err != nil || modelsResp == nil || modelsResp.Data == nil {
			continue
		}

		for _, model := range modelsResp.Data {
			if model.ID == modelID || model.ID == fmt.Sprintf("%s/%s", provider, modelID) {
				// Found the model, assume it supports embeddings if we got here
				// In a real implementation, we'd check model metadata
				return connector, provider, nil
			}
		}
	}

	return nil, "", ErrEmbeddingsNotSupported
}

// extractProviderFromModelID extracts the provider from a model ID
func extractProviderFromModelID(modelID string) string {
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
