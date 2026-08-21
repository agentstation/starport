// Package proxy provides a high-performance LLM request proxy with support for
// multiple providers, intelligent routing, caching, and extensible middleware.
package proxy

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/routing"
)

// Config holds the configuration for creating a new proxy service.
type Config struct {
	// Registry provides access to LLM provider connectors
	Registry connectors.LeasingRegistry

	// Router handles intelligent model selection and failover
	Router router.ModelRouter

	// CacheManager handles response caching (optional)
	CacheManager CacheManager

	// CacheConfig configures caching behavior (optional)
	CacheConfig *CacheConfig

	// Middlewares to apply to the proxy service
	Middlewares []Middleware
}

// Option configures the proxy service.
type Option func(*Config)

// WithCache enables caching with the specified cache manager and configuration.
func WithCache(manager CacheManager, config *CacheConfig) Option {
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
func New(registry connectors.LeasingRegistry, router router.ModelRouter, opts ...Option) Proxy {
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
		var generationSource runtimeGenerationSource
		if cfg.Registry != nil {
			generationSource = cfg.Registry
		}
		cacheMiddleware := NewCacheMiddleware(cfg.CacheManager, cfg.CacheConfig, generationSource)
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

func transformProviderPreferences(prefs *ProviderPreferences) *router.ProviderPreferences {
	if prefs == nil {
		return nil
	}

	return &router.ProviderPreferences{
		Order:          append([]string(nil), prefs.Order...),
		Only:           append([]string(nil), prefs.Only...),
		Ignore:         append([]string(nil), prefs.Ignore...),
		AllowFallbacks: prefs.AllowFallback,
	}
}

func transformAPIKeyConfig(config *APIKeyRoutingConfig) *router.APIKeyConfig {
	if config == nil {
		return nil
	}

	modelOverrides := make(map[string]string, len(config.ModelOverrides))
	for model, override := range config.ModelOverrides {
		modelOverrides[model] = override
	}
	if len(modelOverrides) == 0 {
		modelOverrides = nil
	}

	return &router.APIKeyConfig{
		AllowedProviders:   append([]string(nil), config.AllowedProviders...),
		AllowedModels:      append([]string(nil), config.AllowedModels...),
		ModelOverrides:     modelOverrides,
		RateLimitTier:      config.RateLimitTier,
		CredentialStrategy: config.CredentialStrategy,
	}
}

func buildRequestMetadata(req *ChatCompletionRequest) *router.RequestMetadata {
	if req == nil {
		return nil
	}

	metadata := &router.RequestMetadata{
		EstimatedTokens:  estimatePromptTokens(req.Request.Messages),
		RequiredFeatures: requiredFeatures(req),
	}
	if req.Request.User != "" {
		// Starport does not currently expose a separate conversation ID field.
		// OpenAI's user field is the closest stable affinity key available.
		metadata.ConversationID = req.Request.User
		metadata.UserPreferences = map[string]any{"user": req.Request.User}
	}

	if metadata.EstimatedTokens == 0 && len(metadata.RequiredFeatures) == 0 && metadata.ConversationID == "" {
		return nil
	}
	return metadata
}

func estimatePromptTokens(messages []inference.Message) int {
	total := 0
	for _, msg := range messages {
		for _, part := range msg.Content {
			total += estimateStringTokens(part.Text)
			if part.Image != nil && part.Image.URL != "" {
				total += 85
			}
		}
		total += estimateStringTokens(msg.Name)
		total += estimateStringTokens(msg.Reasoning)
	}
	return total
}

func estimateStringTokens(value string) int {
	if value == "" {
		return 0
	}
	tokens := len(value) / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}

func requiredFeatures(req *ChatCompletionRequest) []string {
	var features []string
	if len(req.Request.Tools) > 0 {
		features = append(features, "function_calling")
	}
	if requestHasVision(req.Request.Messages) {
		features = append(features, "vision")
	}
	return features
}

func requestHasVision(messages []inference.Message) bool {
	for _, msg := range messages {
		for _, part := range msg.Content {
			if part.Kind == inference.ContentImage || part.Image != nil {
				return true
			}
		}
	}
	return false
}

// proxy implements the Proxy interface
type proxy struct {
	registry connectors.LeasingRegistry
	router   router.ModelRouter
}

// ProcessChatCompletion handles chat completion requests with routing
func (p *proxy) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Validate request
	if err := ValidateChatCompletionRequest(req); err != nil {
		return nil, err
	}

	// Transform to connector request
	connReq, err := TransformChatRequest(req)
	if err != nil {
		return nil, &ValidationError{Field: "request", Message: err.Error()}
	}

	// Check if request has cache control
	hasCacheControl := false
	for _, msg := range connReq.Messages {
		if connectors.HasCacheControl(msg.Content) {
			hasCacheControl = true
			break
		}
	}

	// Create routing request
	routingReq := &router.Request{
		ChatRequest:         connReq,
		Models:              req.Request.FallbackModels,
		ProviderPreferences: transformProviderPreferences(req.Provider),
		APIKeyConfig:        transformAPIKeyConfig(req.APIKeyConfig),
		Metadata:            buildRequestMetadata(req),
		TenantID:            req.TenantID,
	}
	if hasCacheControl {
		routingReq.PrepareAttempt = func(route routing.Route, attempt *connectors.ChatRequest) *connectors.ChatRequest {
			if route.PromptCacheKnown && route.PromptCache {
				return attempt
			}

			attemptCopy := *attempt
			attemptCopy.Messages = stripCacheControlFromMessages(attempt.Messages)
			return &attemptCopy
		}
	}

	// Route the request with fallback
	result, err := p.router.RouteWithFallback(ctx, routingReq)
	if err != nil {
		return nil, &RoutingError{
			Model:  req.Request.Model,
			Reason: "failed to route request",
			Err:    connectors.NormalizeFailure("", err),
		}
	}

	// Transform response
	proxyResp, err := TransformChatResponse(result.ChatResponse, result.ModelUsed)
	if err != nil {
		return nil, fmt.Errorf("transform routed chat response: %w", err)
	}

	// Retain the exact route that produced the canonical response.
	proxyResp.Response.ModelUsed = result.ModelUsed

	// Calculate cache costs if cache control was used
	if hasCacheControl && result.ChatResponse != nil && result.Usage.PromptTokens > 0 {
		if writeRate, _, ok := cacheTokenPrices(result.CatalogSnapshot, result.ModelUsed); ok {
			promptTokens := float64(result.Usage.PromptTokens)
			writeCost := promptTokens * writeRate
			readCost := 0.0
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
	if !req.Request.Stream {
		return nil, ErrStreamingNotSupported
	}

	// Transform to connector request
	connReq, err := TransformChatRequest(req)
	if err != nil {
		return nil, &ValidationError{Field: "request", Message: err.Error()}
	}
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
	routingReq := &router.Request{
		ChatRequest:         connReq,
		Models:              req.Request.FallbackModels,
		ProviderPreferences: transformProviderPreferences(req.Provider),
		APIKeyConfig:        transformAPIKeyConfig(req.APIKeyConfig),
		Metadata:            buildRequestMetadata(req),
		TenantID:            req.TenantID,
	}
	if hasCacheControl {
		routingReq.PrepareAttempt = func(route routing.Route, attempt *connectors.ChatRequest) *connectors.ChatRequest {
			if route.PromptCacheKnown && route.PromptCache {
				return attempt
			}
			attemptCopy := *attempt
			attemptCopy.Messages = stripCacheControlFromMessages(attempt.Messages)
			return &attemptCopy
		}
	}

	stream, err := p.router.RouteStream(ctx, routingReq)
	if err != nil {
		return nil, &RoutingError{
			Model:  req.Request.Model,
			Reason: "failed to execute stream route",
			Err:    err,
		}
	}
	return stream, nil
}

func stripCacheControlFromMessages(messages []connectors.Message) []connectors.Message {
	strippedMessages := make([]connectors.Message, len(messages))
	for i, msg := range messages {
		strippedContent, err := connectors.StripCacheControl(msg.Content)
		if err != nil {
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
	return strippedMessages
}

// ProcessEmbeddings handles embedding generation requests
func (p *proxy) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Validate request
	if err := ValidateEmbeddingsRequest(req); err != nil {
		return nil, err
	}

	// Transform to connector request
	connReq := TransformEmbeddingsRequest(req)

	result, err := p.router.RouteEmbeddings(ctx, &router.EmbeddingRequest{
		EmbeddingsRequest: connReq,
		APIKeyConfig:      transformAPIKeyConfig(req.APIKeyConfig),
		TenantID:          req.TenantID,
	})
	if err != nil {
		return nil, &RoutingError{
			Model: req.Request.Model, Reason: "failed to route embedding request", Err: err,
		}
	}
	return &EmbeddingsResponse{Response: result.Response}, nil
}

// ListModels returns models from one retained routable catalog generation.
func (p *proxy) ListModels(ctx context.Context) (*ModelsResponse, error) {
	if p == nil || p.registry == nil {
		return nil, runtimecatalog.ErrCatalogRequired
	}
	runtime, owned, err := p.acquireRuntime(ctx)
	if err != nil {
		return nil, runtimecatalog.ErrCatalogRequired
	}
	if owned {
		defer runtime.Release()
	}
	snapshot := runtime.Snapshot()
	if snapshot == nil {
		return nil, runtimecatalog.ErrCatalogRequired
	}
	return modelsResponseFromSnapshot(snapshot), nil
}

func modelsResponseFromSnapshot(snapshot *runtimecatalog.RoutableSnapshot) *ModelsResponse {
	if snapshot == nil {
		return &ModelsResponse{Object: "list"}
	}
	definitions := snapshot.Definitions()
	models := make([]ModelInfo, 0, len(definitions))
	for _, definition := range definitions {
		created := definition.CreatedAt.Unix()
		if !definition.Metadata.ReleaseDate.IsZero() {
			created = definition.Metadata.ReleaseDate.Unix()
		}
		ownedBy := ""
		if len(definition.AuthorIDs) > 0 {
			ownedBy = string(definition.AuthorIDs[0])
		}
		model := ModelInfo{
			ID:      string(definition.ID),
			Object:  "model",
			Created: created,
			OwnedBy: ownedBy,
		}
		enrichModelInfo(snapshot, definition, &model)
		models = append(models, model)
	}
	return &ModelsResponse{Object: "list", Data: models}
}

func enrichModelInfo(
	snapshot *runtimecatalog.RoutableSnapshot,
	definition starmapcatalogs.ModelDefinition,
	model *ModelInfo,
) {
	if snapshot == nil || model == nil {
		return
	}
	model.CanonicalSlug = string(definition.ID)
	model.Name = definition.Name
	model.Description = definition.Description
	if definition.Weights.Architecture != nil {
		model.Architecture = &ModelArchitecture{
			InputModalities:  modelInputModalities(definition),
			OutputModalities: modelOutputModalities(definition),
			Tokenizer:        definition.Weights.Architecture.Tokenizer.String(),
		}
	} else {
		model.Architecture = &ModelArchitecture{
			InputModalities:  modelInputModalities(definition),
			OutputModalities: modelOutputModalities(definition),
		}
	}
	model.SupportedParameters = supportedModelParameters(definition)
	routes := snapshot.RoutesForDefinition(definition.ID)
	if len(routes) == 0 {
		return
	}
	offering, err := snapshot.Offering(routes[0])
	if err != nil {
		return
	}
	if offering.Limits != nil {
		contextLength := boundedModelInt(offering.Limits.ContextWindow)
		model.Context = &contextLength
		model.TopProvider = &TopProviderInfo{
			ContextLength:       contextLength,
			MaxCompletionTokens: boundedModelInt(offering.Limits.OutputTokens),
		}
	}
	if offering.Pricing != nil && offering.Pricing.Tokens != nil {
		model.Pricing = &ModelPricing{Currency: offering.Pricing.Currency.String()}
		if offering.Pricing.Tokens.Input != nil {
			model.Pricing.Prompt = formatTokenPrice(offering.Pricing.Tokens.Input)
		}
		if offering.Pricing.Tokens.Output != nil {
			model.Pricing.Completion = formatTokenPrice(offering.Pricing.Tokens.Output)
		}
	}
}

func formatTokenPrice(cost *starmapcatalogs.ModelTokenCost) string {
	if cost == nil {
		return ""
	}
	value := cost.PerToken
	if value == 0 && cost.Per1M != 0 {
		value = cost.Per1M / 1_000_000
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func boundedModelInt(value int64) int {
	maxInt := int64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func modelInputModalities(definition starmapcatalogs.ModelDefinition) []string {
	if definition.Capabilities.Features == nil {
		return nil
	}
	result := make([]string, 0, len(definition.Capabilities.Features.Modalities.Input))
	for _, modality := range definition.Capabilities.Features.Modalities.Input {
		result = append(result, modality.String())
	}
	return result
}

func modelOutputModalities(definition starmapcatalogs.ModelDefinition) []string {
	if definition.Capabilities.Features == nil {
		return nil
	}
	result := make([]string, 0, len(definition.Capabilities.Features.Modalities.Output))
	for _, modality := range definition.Capabilities.Features.Modalities.Output {
		result = append(result, modality.String())
	}
	return result
}

func supportedModelParameters(definition starmapcatalogs.ModelDefinition) []string {
	features := definition.Capabilities.Features
	if features == nil {
		return nil
	}
	parameters := make([]string, 0, 16)
	for _, item := range []struct {
		name      string
		supported bool
	}{
		{"tools", features.Tools},
		{"tool_choice", features.ToolChoice},
		{"reasoning", features.Reasoning},
		{"reasoning_effort", features.ReasoningEffort},
		{"temperature", features.Temperature},
		{"top_p", features.TopP},
		{"top_k", features.TopK},
		{"max_tokens", features.MaxTokens || features.MaxOutputTokens},
		{"stop", features.Stop},
		{"frequency_penalty", features.FrequencyPenalty},
		{"presence_penalty", features.PresencePenalty},
		{"logit_bias", features.LogitBias},
		{"seed", features.Seed},
		{"logprobs", features.Logprobs},
		{"top_logprobs", features.TopLogprobs},
		{"n", features.N},
		{"response_format", features.StructuredOutputs},
	} {
		if item.supported {
			parameters = append(parameters, item.name)
		}
	}
	return parameters
}

// ListProviders returns available provider information
func (p *proxy) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	runtime, owned, err := p.acquireRuntime(ctx)
	if err != nil {
		return nil, connectors.ErrRuntimeUnavailable
	}
	if owned {
		defer runtime.Release()
	}
	return &ProvidersResponse{Providers: providerInfosFromRuntime(runtime)}, nil
}

func providerInfosFromRuntime(runtime connectors.RuntimeLease) []ProviderInfo {
	if runtime == nil {
		return nil
	}
	snapshot := runtime.Snapshot()
	if snapshot == nil {
		return nil
	}
	seen := make(map[starmapcatalogs.ProviderID]struct{})
	providers := make([]ProviderInfo, 0)
	for _, route := range snapshot.Routes() {
		if _, exists := seen[route.ProviderID]; exists {
			continue
		}
		provider, err := snapshot.Catalog().Provider(route.ProviderID)
		if err != nil {
			continue
		}
		info := ProviderInfo{
			ID: string(provider.ID), Name: provider.Name,
			RequiresAuth:     runtime.RequiresAuthentication(string(provider.ID)),
			CredentialFields: inferenceCredentialFields(provider),
		}
		if provider.StatusPageURL != nil {
			info.URL = *provider.StatusPageURL
		}
		capabilities := make(map[string]struct{})
		for _, providerRoute := range snapshot.RoutesForProvider(route.ProviderID) {
			info.Models = append(info.Models, providerRoute.ID())
			for _, operation := range providerRoute.Operations {
				capabilities[string(operation)] = struct{}{}
			}
		}
		for capability := range capabilities {
			info.Capabilities = append(info.Capabilities, capability)
		}
		sort.Strings(info.Capabilities)
		seen[route.ProviderID] = struct{}{}
		providers = append(providers, info)
	}
	return providers
}

// inferenceCredentialFields projects the catalog's inference credential
// contract in profile order, deduplicated across alternatives.
func inferenceCredentialFields(provider starmapcatalogs.Provider) []CredentialFieldInfo {
	contract := provider.Credentials
	if contract == nil {
		return nil
	}
	fields := make(map[starmapcatalogs.ProviderCredentialFieldID]starmapcatalogs.ProviderCredentialField, len(contract.Fields))
	for _, field := range contract.Fields {
		fields[field.ID] = field
	}
	profiles := make(map[starmapcatalogs.ProviderCredentialProfileID]starmapcatalogs.ProviderCredentialProfile, len(contract.Profiles))
	for _, profile := range contract.Profiles {
		profiles[profile.ID] = profile
	}
	seen := make(map[starmapcatalogs.ProviderCredentialFieldID]struct{})
	infos := make([]CredentialFieldInfo, 0)
	for _, profileID := range contract.Inference.Alternatives {
		profile, exists := profiles[profileID]
		if !exists {
			continue
		}
		for _, fieldID := range profile.Fields {
			if _, exists := seen[fieldID]; exists {
				continue
			}
			field, exists := fields[fieldID]
			if !exists {
				continue
			}
			seen[fieldID] = struct{}{}
			infos = append(infos, CredentialFieldInfo{
				ID:          string(field.ID),
				Kind:        string(field.Kind),
				Required:    field.Required,
				Default:     field.Default,
				Description: field.Description,
			})
		}
	}
	return infos
}

func cacheTokenPrices(snapshot *runtimecatalog.RoutableSnapshot, modelID string) (float64, float64, bool) {
	if snapshot == nil {
		return 0, 0, false
	}
	route, ok := snapshot.ResolveRoute(modelID)
	if !ok {
		return 0, 0, false
	}
	offering, err := snapshot.Offering(route)
	if err != nil || !offering.Supports(starmapcatalogs.ProviderOperationChatCompletions) ||
		offering.Service.PromptCache == nil || !*offering.Service.PromptCache ||
		offering.Pricing == nil || offering.Pricing.Tokens == nil {
		return 0, 0, false
	}
	tokens := offering.Pricing.Tokens
	if tokens.CacheWrite == nil && tokens.CacheRead == nil {
		return 0, 0, false
	}
	return modelTokenPrice(tokens.CacheWrite), modelTokenPrice(tokens.CacheRead), true
}

func modelTokenPrice(cost *starmapcatalogs.ModelTokenCost) float64 {
	if cost == nil {
		return 0
	}
	if cost.PerToken != 0 {
		return cost.PerToken
	}
	return cost.Per1M / 1_000_000
}

// GetModelEndpoints returns provider endpoints for a specific model
func (p *proxy) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	if p == nil || p.registry == nil {
		return nil, runtimecatalog.ErrCatalogRequired
	}
	runtime, owned, err := p.acquireRuntime(ctx)
	if err != nil {
		return nil, runtimecatalog.ErrCatalogRequired
	}
	if owned {
		defer runtime.Release()
	}
	snapshot := runtime.Snapshot()
	if snapshot == nil {
		return nil, runtimecatalog.ErrCatalogRequired
	}
	endpoints := make([]EndpointInfo, 0)
	for _, route := range snapshot.Routes() {
		if route.ID() != modelID && string(route.DefinitionID) != modelID {
			continue
		}
		endpoint, found := route.Endpoint(starmapcatalogs.ProviderOperationChatCompletions)
		if !found {
			continue
		}
		info := EndpointInfo{
			Provider: string(route.ProviderID), Endpoint: endpoint.URL, Available: true,
		}
		offering, err := snapshot.Offering(route)
		if err == nil && offering.Pricing != nil && offering.Pricing.Tokens != nil {
			info.CostPrompt = formatTokenPrice(offering.Pricing.Tokens.Input)
			info.CostOutput = formatTokenPrice(offering.Pricing.Tokens.Output)
		}
		endpoints = append(endpoints, info)
	}

	return &ModelEndpointsResponse{
		Model:     modelID,
		Endpoints: endpoints,
	}, nil
}

func (p *proxy) acquireRuntime(
	ctx context.Context,
) (connectors.RuntimeLease, bool, error) {
	if lease := connectors.RuntimeLeaseFromContext(ctx); lease != nil {
		return lease, false, nil
	}
	if p == nil || p.registry == nil {
		return nil, false, connectors.ErrRuntimeUnavailable
	}
	lease, err := p.registry.AcquireRuntime()
	return lease, true, err
}
