// Package proxy provides a high-performance LLM request proxy with support for
// multiple providers, intelligent routing, caching, and extensible middleware.
package proxy

import (
	"context"
	"errors"
	"fmt"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/tokenize"
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

	// TokenEstimator synthesizes estimated usage for streams that end
	// without provider-reported usage (optional)
	TokenEstimator *tokenize.Estimator

	// FileResolver reads the bytes behind a stored document reference
	// (optional). A deployment without one refuses a request that names a
	// file rather than sending the model an empty document.
	FileResolver FileResolver

	// DocumentExtractor is the native parser engine, with the page and time
	// bounds this deployment sets (optional). A deployment without one reads
	// documents under the engine's own default bounds.
	DocumentExtractor *document.Extractor

	// DocumentCache holds one document's text for reuse inside a window
	// (optional). A deployment without one reads every attachment on every
	// turn, which is correct and pays the page price each time.
	DocumentCache *document.Cache
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

// WithFiles lets a chat request name a document this gateway already stores
// instead of carrying its bytes.
func WithFiles(resolver FileResolver) Option {
	return func(c *Config) {
		c.FileResolver = resolver
	}
}

// WithDocumentExtractor sets the bounds the native parser engine reads under.
func WithDocumentExtractor(extractor *document.Extractor) Option {
	return func(c *Config) {
		c.DocumentExtractor = extractor
	}
}

// WithDocumentCache reuses one document's text across the turns of a
// conversation that keeps resending it.
func WithDocumentCache(cache *document.Cache) Option {
	return func(c *Config) {
		c.DocumentCache = cache
	}
}

// WithTokenEstimator guarantees every chat stream ends with a usage event:
// when the provider reports none, the estimator synthesizes estimated counts.
func WithTokenEstimator(estimator *tokenize.Estimator) Option {
	return func(c *Config) {
		c.TokenEstimator = estimator
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
		registry:  cfg.Registry,
		router:    cfg.Router,
		estimator: cfg.TokenEstimator,
		files:     cfg.FileResolver,
		documents: cfg.DocumentExtractor,

		extractions: cfg.DocumentCache,
	}

	var p Proxy = core

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

func transformProviderPreferences(prefs *ProviderPreferences) *router.ProviderPreferences {
	if prefs == nil {
		return nil
	}

	return &router.ProviderPreferences{
		Order:                   append([]string(nil), prefs.Order...),
		Only:                    append([]string(nil), prefs.Only...),
		Ignore:                  append([]string(nil), prefs.Ignore...),
		AllowFallbacks:          prefs.AllowFallback,
		Sort:                    prefs.Sort,
		MaxPromptPricePer1M:     prefs.MaxPromptPricePer1M,
		MaxCompletionPricePer1M: prefs.MaxCompletionPricePer1M,
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

func (p *proxy) buildRequestMetadata(req *ChatCompletionRequest) *router.RequestMetadata {
	if req == nil {
		return nil
	}

	modalities := requestModalities(req.Request.Messages)
	metadata := &router.RequestMetadata{
		EstimatedTokens:    p.estimatePromptTokens(req),
		RequiredFeatures:   requiredFeatures(req, modalities),
		RequiredModalities: modalities,
	}
	if req.Request.User != "" {
		// Starport does not currently expose a separate conversation ID field.
		// OpenAI's user field is the closest stable affinity key available.
		metadata.ConversationID = req.Request.User
		metadata.UserPreferences = map[string]any{"user": req.Request.User}
	}

	if metadata.EstimatedTokens == 0 && len(metadata.RequiredFeatures) == 0 &&
		len(metadata.RequiredModalities) == 0 && metadata.ConversationID == "" {
		return nil
	}
	return metadata
}

// estimatePromptTokens counts the prompt with the estimator that also counts
// usage when a provider reports none. Two estimators over one request gave
// two token counts for the same messages, so the route decision and the
// usage record disagreed about the size of the same call.
func (p *proxy) estimatePromptTokens(req *ChatCompletionRequest) int {
	hint := tokenize.Hint{Model: req.Request.Model}
	return p.tokenEstimator().CountMessages(hint, req.Request.Messages)
}

// tokenEstimator returns the composed estimator, or a codec-free one that
// falls back to the bytes-per-token heuristic. A proxy built without an
// estimator still routes; it just counts more coarsely.
func (p *proxy) tokenEstimator() *tokenize.Estimator {
	if p.estimator != nil {
		return p.estimator
	}
	return heuristicEstimator
}

// heuristicEstimator holds no codecs, so every count it makes falls back to
// the bytes-per-token heuristic. Building it costs nothing, which is why it
// can be a package value.
var heuristicEstimator = &tokenize.Estimator{}

// requestModalities names the media the request carries. It reads the same
// canonical walk the media accounting reads, so a route decision and a usage
// record cannot disagree about which media a request held.
func requestModalities(messages []inference.Message) []string {
	modalities := inference.RequestMediaModalities(messages)
	if len(modalities) == 0 {
		return nil
	}
	names := make([]string, 0, len(modalities))
	for _, modality := range modalities {
		names = append(names, string(modality))
	}
	return names
}

// requiredFeatures names the model features the request needs. Vision is one
// of them, and it now reads the modality list rather than repeating the
// image test, so image is described in one place.
func requiredFeatures(req *ChatCompletionRequest, modalities []string) []string {
	var features []string
	if len(req.Request.Tools) > 0 {
		features = append(features, "function_calling")
	}
	for _, modality := range modalities {
		if modality == string(inference.ModalityImage) {
			features = append(features, "vision")
			break
		}
	}
	return features
}

// modalityRefusal converts a planner modality refusal into a caller-facing
// validation error. Every other routing failure is a gateway or provider
// condition and answers 503, but a model that cannot read the media the
// caller sent is a caller mistake, and a retry against the same model will
// fail the same way.
func modalityRefusal(err error) error {
	if !errors.Is(err, routing.ErrModalityUnsupported) {
		return nil
	}
	return &ValidationError{Field: fieldMessages, Message: err.Error()}
}

// proxy implements the Proxy interface
type proxy struct {
	registry  connectors.LeasingRegistry
	router    router.ModelRouter
	estimator *tokenize.Estimator
	files     FileResolver
	documents *document.Extractor

	// extractions holds text a parser engine already read, so a conversation
	// that resends the same attachment on every turn pays for it once.
	extractions *document.Cache

	// prices answers what one page of a document and one search unit cost. It
	// is unset in every deployment, where the catalog the request carries
	// answers instead; a caller that reaches this proxy without a runtime lease
	// sets it.
	prices catalogPrices
}

// ProcessChatCompletion handles chat completion requests with routing
func (p *proxy) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Validate request
	if err := ValidateChatCompletionRequest(req); err != nil {
		return nil, err
	}

	// Read every stored document once, before the attempt loop the router
	// runs below. Everything after this point sees the request an inline
	// caller would have sent.
	req, err := p.resolveDocuments(ctx, req)
	if err != nil {
		return nil, err
	}

	// The route is planned from the request the caller sent, and the parser
	// below rewrites the request the provider receives. Reading the metadata
	// first is what keeps those two facts separate: a document turned into
	// text no longer looks like a document, and a route planned after the
	// rewrite would land on a text-only model the caller never asked for.
	keyConfig := transformAPIKeyConfig(req.APIKeyConfig)
	metadata := p.buildRequestMetadata(req)

	parsed, parsing, err := p.parseDocuments(ctx, req, keyConfig)
	if err != nil {
		return nil, err
	}

	// Transform to connector request
	connReq, err := TransformChatRequest(parsed)
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
		APIKeyConfig:        keyConfig,
		Metadata:            metadata,
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
		if refusal := modalityRefusal(err); refusal != nil {
			return nil, refusal
		}
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
	parsing.report(proxyResp)
	proxyResp.Response.ModelUsed = result.ModelUsed
	proxyResp.ProviderUsed = result.ProviderUsed
	proxyResp.CredentialSource = result.CredentialSource
	proxyResp.Attempts = result.Attempts
	proxyResp.CatalogSnapshot = result.CatalogSnapshot
	if result.Metadata != nil {
		proxyResp.RoutingDuration = result.Metadata.RoutingDuration
	}

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

	// Read every stored document once, before the attempt loop the router
	// runs below. A stream retries the same way a completion does.
	req, err := p.resolveDocuments(ctx, req)
	if err != nil {
		return nil, err
	}

	// A stream plans its route from the caller's own request for the same
	// reason a completion does. See ProcessChatCompletion above.
	keyConfig := transformAPIKeyConfig(req.APIKeyConfig)
	metadata := p.buildRequestMetadata(req)

	// A stream reports no extraction hit. The saving is real and the stream
	// has nowhere to state it: the first event a caller reads is a token of
	// the answer, and by then the document is long read.
	parsed, _, err := p.parseDocuments(ctx, req, keyConfig)
	if err != nil {
		return nil, err
	}

	// Transform to connector request
	connReq, err := TransformChatRequest(parsed)
	if err != nil {
		return nil, &ValidationError{Field: "request", Message: err.Error()}
	}
	connReq.Stream = true
	// Ask the provider for measured usage on every stream. Providers
	// that honor stream_options report exact counts; the estimator
	// below covers the rest.
	connReq.StreamOptions = &connectors.StreamOptions{IncludeUsage: true}

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
		APIKeyConfig:        keyConfig,
		Metadata:            metadata,
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
		if refusal := modalityRefusal(err); refusal != nil {
			return nil, refusal
		}
		return nil, &RoutingError{
			Model:  req.Request.Model,
			Reason: "failed to execute stream route",
			Err:    err,
		}
	}
	return newUsageNormalizingStream(stream, req.Request.Messages, p.estimator), nil
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
	response := &EmbeddingsResponse{
		Response:         result.Response,
		ModelUsed:        result.ModelUsed,
		ProviderUsed:     result.ProviderUsed,
		CredentialSource: result.CredentialSource,
		Attempts:         result.Attempts,
		CatalogSnapshot:  result.CatalogSnapshot,
	}
	if result.Metadata != nil {
		response.RoutingDuration = result.Metadata.RoutingDuration
	}
	return response, nil
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
	return &ModelsResponse{Object: "list", Data: view.Models(snapshot)}
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

// ListAuthors returns catalog author information
func (p *proxy) ListAuthors(ctx context.Context) (*AuthorsResponse, error) {
	snapshot, release, err := p.acquireSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return &AuthorsResponse{Authors: view.Authors(snapshot)}, nil
}

// GetAuthor returns one catalog author by ID
func (p *proxy) GetAuthor(ctx context.Context, authorID string) (*AuthorInfo, error) {
	snapshot, release, err := p.acquireSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	author, ok := view.AuthorByID(snapshot, authorID)
	if !ok {
		return nil, &ProviderError{Code: "not_found", Message: "Author not found"}
	}
	return &author, nil
}

// GetLogo returns the catalog-carried SVG brand mark for one provider or author.
func (p *proxy) GetLogo(ctx context.Context, kind view.LogoKind, id string) ([]byte, error) {
	snapshot, release, err := p.acquireSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	svg, ok := view.Logo(snapshot, kind, id)
	if !ok {
		return nil, &ProviderError{Code: "not_found", Message: "Logo not found"}
	}
	return svg, nil
}

// acquireSnapshot leases the runtime and returns its catalog snapshot with
// a release func that is always safe to defer.
func (p *proxy) acquireSnapshot(ctx context.Context) (*runtimecatalog.RoutableSnapshot, func(), error) {
	runtime, owned, err := p.acquireRuntime(ctx)
	if err != nil {
		return nil, nil, runtimecatalog.ErrCatalogRequired
	}
	release := func() {}
	if owned {
		release = runtime.Release
	}
	snapshot := runtime.Snapshot()
	if snapshot == nil {
		release()
		return nil, nil, runtimecatalog.ErrCatalogRequired
	}
	return snapshot, release, nil
}

func providerInfosFromRuntime(runtime connectors.RuntimeLease) []ProviderInfo {
	if runtime == nil {
		return nil
	}
	return view.Providers(runtime.Snapshot(), runtime.RequiresAuthentication)
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
	return &ModelEndpointsResponse{
		Model:     modelID,
		Endpoints: view.Endpoints(snapshot, modelID),
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
