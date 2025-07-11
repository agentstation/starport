package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentstation/starport/internal/cache"
	"github.com/rs/zerolog/log"
)

// Define typed context keys
type contextKey string

const (
	// CacheStatusKey is the context key for cache status
	CacheStatusKey contextKey = "X-Cache"
)

// CachedService wraps a Service with caching capabilities
type CachedService struct {
	service     Service
	cache       cache.Cache
	keyGen      *cache.KeyGenerator
	cacheConfig CacheConfig
}

// CacheConfig defines caching behavior
type CacheConfig struct {
	// Enable caching for different endpoints
	EnableChatCache      bool `env:"ENABLE_CHAT_CACHE,default=true"`
	EnableEmbeddingCache bool `env:"ENABLE_EMBEDDING_CACHE,default=true"`
	EnableModelCache     bool `env:"ENABLE_MODEL_CACHE,default=true"`
	EnableProviderCache  bool `env:"ENABLE_PROVIDER_CACHE,default=true"`
	// Skip cache for specific models or patterns
	SkipCacheModels []string `env:"SKIP_CACHE_MODELS"`
	// Force cache refresh header
	CacheControlHeader string `env:"CACHE_CONTROL_HEADER,default=X-Cache-Control"`
}

// NewCachedService creates a new cached service wrapper
func NewCachedService(service Service, c cache.Cache, config CacheConfig) Service {
	return &CachedService{
		service:     service,
		cache:       c,
		keyGen:      cache.NewKeyGenerator("starport"),
		cacheConfig: config,
	}
}

// ProcessChatCompletion handles chat completions with caching
func (s *CachedService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Skip cache for streaming requests or if caching is disabled
	if req.Stream || !s.cacheConfig.EnableChatCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessChatCompletion(ctx, req)
	}

	// Convert to cache request type
	cacheReq := toCacheChatRequest(req)

	// Generate cache key
	cacheKey := s.keyGen.ChatCompletionKey(cacheReq)

	// Try to get from cache
	if cachedData, found, err := s.cache.Get(ctx, cacheKey); found && err == nil {
		var resp ChatCompletionResponse
		if err := json.Unmarshal(cachedData, &resp); err == nil {
			// Cache hit - would set context but we're returning early
			// TODO: Add cache status to response struct or headers

			log.Debug().
				Str("cache_key", cacheKey).
				Str("model", req.Model).
				Msg("cache hit for chat completion")

			return &resp, nil
		}
	}

	// Cache miss - call underlying service
	ctx = context.WithValue(ctx, CacheStatusKey, "MISS")
	resp, err := s.service.ProcessChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	// Cache successful responses
	if respData, err := json.Marshal(resp); err == nil {
		policy := cache.DefaultPolicies()[cache.PolicyTypeChatCompletion]
		if err := s.cache.Set(ctx, cacheKey, respData, policy.TTL); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache chat completion response")
		}
	}

	return resp, nil
}

// ProcessChatCompletionStream handles streaming requests (no caching)
func (s *CachedService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	// Streaming responses are never cached
	return s.service.ProcessChatCompletionStream(ctx, req)
}

// ProcessEmbeddings handles embeddings with caching
func (s *CachedService) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Skip cache if disabled
	if !s.cacheConfig.EnableEmbeddingCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessEmbeddings(ctx, req)
	}

	// Convert to cache request type
	cacheReq := toCacheEmbeddingRequest(req)

	// Generate cache key
	cacheKey := s.keyGen.EmbeddingKey(cacheReq)

	// Try to get from cache
	if cachedData, found, err := s.cache.Get(ctx, cacheKey); found && err == nil {
		var resp EmbeddingsResponse
		if err := json.Unmarshal(cachedData, &resp); err == nil {
			// Cache hit - would set context but we're returning early
			// TODO: Add cache status to response struct or headers

			log.Debug().
				Str("cache_key", cacheKey).
				Str("model", req.Model).
				Msg("cache hit for embedding")
			return &resp, nil
		}
	}

	// Cache miss
	ctx = context.WithValue(ctx, CacheStatusKey, "MISS")
	resp, err := s.service.ProcessEmbeddings(ctx, req)
	if err != nil {
		return nil, err
	}

	// Cache successful responses
	if respData, err := json.Marshal(resp); err == nil {
		policy := cache.DefaultPolicies()[cache.PolicyTypeEmbedding]
		if err := s.cache.Set(ctx, cacheKey, respData, policy.TTL); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache embedding response")
		}
	}

	return resp, nil
}

// ListModels returns available models with caching
func (s *CachedService) ListModels(ctx context.Context) (*ModelsResponse, error) {
	if !s.cacheConfig.EnableModelCache || s.shouldSkipCache(ctx, "") {
		return s.service.ListModels(ctx)
	}

	// Generate cache key
	cacheKey := s.keyGen.ModelListKey("")

	// Try to get from cache
	if cachedData, found, err := s.cache.Get(ctx, cacheKey); found && err == nil {
		var resp ModelsResponse
		if err := json.Unmarshal(cachedData, &resp); err == nil {
			// Cache hit - would set context but we're returning early
			// TODO: Add cache status to response struct or headers
			return &resp, nil
		}
	}

	// Cache miss
	ctx = context.WithValue(ctx, CacheStatusKey, "MISS")
	resp, err := s.service.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	// Cache successful responses
	if respData, err := json.Marshal(resp); err == nil {
		policy := cache.DefaultPolicies()[cache.PolicyTypeModel]
		if err := s.cache.Set(ctx, cacheKey, respData, policy.TTL); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache models response")
		}
	}

	return resp, nil
}

// ListProviders returns available providers with caching
func (s *CachedService) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	if !s.cacheConfig.EnableProviderCache || s.shouldSkipCache(ctx, "") {
		return s.service.ListProviders(ctx)
	}

	cacheKey := s.keyGen.ProviderListKey()

	// Try to get from cache
	if cachedData, found, err := s.cache.Get(ctx, cacheKey); found && err == nil {
		var resp ProvidersResponse
		if err := json.Unmarshal(cachedData, &resp); err == nil {
			// Cache hit - would set context but we're returning early
			// TODO: Add cache status to response struct or headers
			return &resp, nil
		}
	}

	// Cache miss
	ctx = context.WithValue(ctx, CacheStatusKey, "MISS")
	resp, err := s.service.ListProviders(ctx)
	if err != nil {
		return nil, err
	}

	// Cache successful responses
	if respData, err := json.Marshal(resp); err == nil {
		policy := cache.DefaultPolicies()[cache.PolicyTypeProvider]
		if err := s.cache.Set(ctx, cacheKey, respData, policy.TTL); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache providers response")
		}
	}

	return resp, nil
}

// GetModelEndpoints returns endpoints for a specific model (no caching needed)
func (s *CachedService) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	// Model endpoints are dynamic based on availability, so we don't cache them
	return s.service.GetModelEndpoints(ctx, modelID)
}

// shouldSkipCache checks if caching should be skipped
func (s *CachedService) shouldSkipCache(ctx context.Context, model string) bool {
	// Check cache control header from context
	if cacheControl, ok := ctx.Value(s.cacheConfig.CacheControlHeader).(string); ok && cacheControl == "no-cache" {
		return true
	}

	// Check if model is in skip list
	if model != "" {
		for _, skipModel := range s.cacheConfig.SkipCacheModels {
			if strings.Contains(model, skipModel) {
				return true
			}
		}
	}

	return false
}

// toCacheChatRequest converts proxy request to cache request
func toCacheChatRequest(req *ChatCompletionRequest) cache.ChatCompletionRequest {
	msgs := make([]cache.Message, len(req.Messages))
	for i, msg := range req.Messages {
		msgs[i] = cache.Message{
			Role:    msg.Role,
			Content: fmt.Sprintf("%v", msg.Content), // Handle string or complex content
		}
	}

	// Convert LogitBias from map[string]int to map[string]float32
	var logitBias map[string]float32
	if req.LogitBias != nil {
		logitBias = make(map[string]float32)
		for k, v := range req.LogitBias {
			logitBias[k] = float32(v)
		}
	}

	// Convert user to pointer
	var user *string
	if req.User != "" {
		user = &req.User
	}

	// Convert tools to interface slice
	var tools []interface{}
	if req.Tools != nil {
		tools = make([]interface{}, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = tool
		}
	}

	// Get the underlying connector request
	connReq := TransformChatRequest(req)

	return cache.ChatCompletionRequest{
		Model:            connReq.Model,
		Messages:         msgs,
		Temperature:      connReq.Temperature,
		MaxTokens:        connReq.MaxTokens,
		TopP:             connReq.TopP,
		N:                nil, // ChatRequest doesn't have N field
		Stop:             connReq.Stop,
		PresencePenalty:  connReq.PresencePenalty,
		FrequencyPenalty: connReq.FrequencyPenalty,
		LogitBias:        logitBias,
		User:             user,
		Seed:             connReq.Seed,
		Tools:            tools,
		ToolChoice:       connReq.ToolChoice,
		ResponseFormat:   connReq.ResponseFormat,
	}
}

// toCacheEmbeddingRequest converts proxy request to cache request
func toCacheEmbeddingRequest(req *EmbeddingsRequest) cache.EmbeddingRequest {
	// Convert strings to pointers
	var encodingFormat *string
	if req.EncodingFormat != "" {
		encodingFormat = &req.EncodingFormat
	}

	var user *string
	if req.User != "" {
		user = &req.User
	}

	// Get the underlying connector request
	connReq := TransformEmbeddingsRequest(req)

	return cache.EmbeddingRequest{
		Model:          connReq.Model,
		Input:          connReq.Input,
		EncodingFormat: encodingFormat,
		Dimensions:     connReq.Dimensions,
		User:           user,
	}
}

// CachedServiceV2 wraps a Service with cache Manager
type CachedServiceV2 struct {
	service      Service
	cacheManager *cache.Manager
	cacheConfig  CacheConfig
}

// NewCachedServiceV2 creates a new cached service with cache Manager
func NewCachedServiceV2(service Service, cm *cache.Manager, config CacheConfig) Service {
	return &CachedServiceV2{
		service:      service,
		cacheManager: cm,
		cacheConfig:  config,
	}
}

// ProcessChatCompletion handles chat completions with Manager-based caching
func (s *CachedServiceV2) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Skip cache for streaming requests or if caching is disabled
	if req.Stream || !s.cacheConfig.EnableChatCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessChatCompletion(ctx, req)
	}

	// For now, skip caching through manager until methods are implemented
	// TODO: Implement cache manager methods for chat completions
	/*
		// Use cache manager for chat completions
		cacheReq := toCacheChatRequest(req)

		// Try to get from cache
		cachedResp, found, err := s.cacheManager.GetChatCompletion(ctx, cacheReq)
		if found && err == nil {
			// Convert cache response to proxy response
			resp := &ChatCompletionResponse{
				ID:                cachedResp.ID,
				Object:            cachedResp.Object,
				Created:           cachedResp.Created,
				Model:             cachedResp.Model,
				Choices:           cachedResp.Choices,
				Usage:             &cachedResp.Usage,
				SystemFingerprint: cachedResp.SystemFingerprint,
			}

			ctx = context.WithValue(ctx, CacheStatusKey, "HIT")
			log.Debug().
				Str("model", req.Model).
				Msg("cache hit for chat completion")
			return resp, nil
		}
	*/

	// Cache miss - call underlying service
	ctx = context.WithValue(ctx, CacheStatusKey, "MISS")
	resp, err := s.service.ProcessChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	// Cache successful responses
	// TODO: Implement cache manager methods
	/*
		cacheResp := &cache.ChatCompletionResponse{
			ID:                resp.ID,
			Object:            resp.Object,
			Created:           resp.Created,
			Model:             resp.Model,
			Choices:           resp.Choices,
			Usage:             *resp.Usage,
			SystemFingerprint: resp.SystemFingerprint,
		}

		if err := s.cacheManager.SetChatCompletion(ctx, cacheReq, cacheResp, 30*time.Minute); err != nil {
			log.Warn().
				Err(err).
				Str("model", req.Model).
				Msg("failed to cache chat completion response")
		}
	*/

	return resp, nil
}

// ProcessChatCompletionStream handles streaming requests (no caching)
func (s *CachedServiceV2) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	return s.service.ProcessChatCompletionStream(ctx, req)
}

// ProcessEmbeddings handles embeddings with Manager-based caching
func (s *CachedServiceV2) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Skip cache if disabled
	if !s.cacheConfig.EnableEmbeddingCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessEmbeddings(ctx, req)
	}

	// For now, skip caching through manager until methods are implemented
	// TODO: Implement cache manager methods for embeddings
	/*
		// Use cache manager for embeddings
		cacheReq := toCacheEmbeddingRequest(req)

		// Try to get from cache
		cachedResp, found, err := s.cacheManager.GetEmbedding(ctx, cacheReq)
		if found && err == nil {
			// Convert cache response to proxy response
			resp := &EmbeddingsResponse{
				Object: cachedResp.Object,
				Data:   cachedResp.Data,
				Model:  cachedResp.Model,
				Usage:  &cachedResp.Usage,
			}

			ctx = context.WithValue(ctx, CacheStatusKey, "HIT")
			log.Debug().
				Str("model", req.Model).
				Msg("cache hit for embedding")
			return resp, nil
		}
	*/

	// Cache miss
	ctx = context.WithValue(ctx, CacheStatusKey, "MISS")
	resp, err := s.service.ProcessEmbeddings(ctx, req)
	if err != nil {
		return nil, err
	}

	// Cache successful responses
	// TODO: Implement cache manager methods
	/*
		cacheResp := &cache.EmbeddingResponse{
			Object: resp.Object,
			Data:   resp.Data,
			Model:  resp.Model,
			Usage:  *resp.Usage,
		}

		if err := s.cacheManager.SetEmbedding(ctx, cacheReq, cacheResp, 1*time.Hour); err != nil {
			log.Warn().
				Err(err).
				Str("model", req.Model).
				Msg("failed to cache embedding response")
		}
	*/

	return resp, nil
}

// ListModels returns available models with Manager-based caching
func (s *CachedServiceV2) ListModels(ctx context.Context) (*ModelsResponse, error) {
	if !s.cacheConfig.EnableModelCache || s.shouldSkipCache(ctx, "") {
		return s.service.ListModels(ctx)
	}

	// For model list, we'll use the basic cache through manager
	// This is a simplified implementation - in production you might want
	// to add model list specific methods to the cache manager
	return s.service.ListModels(ctx)
}

// ListProviders returns available providers
func (s *CachedServiceV2) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	if !s.cacheConfig.EnableProviderCache || s.shouldSkipCache(ctx, "") {
		return s.service.ListProviders(ctx)
	}

	// For provider list, we'll use the basic cache through manager
	return s.service.ListProviders(ctx)
}

// GetModelEndpoints returns endpoints for a specific model
func (s *CachedServiceV2) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	return s.service.GetModelEndpoints(ctx, modelID)
}

// shouldSkipCache checks if caching should be skipped
func (s *CachedServiceV2) shouldSkipCache(ctx context.Context, model string) bool {
	// Check cache control header from context
	if cacheControl, ok := ctx.Value(s.cacheConfig.CacheControlHeader).(string); ok && cacheControl == "no-cache" {
		return true
	}

	// Check if model is in skip list
	if model != "" {
		for _, skipModel := range s.cacheConfig.SkipCacheModels {
			if strings.Contains(model, skipModel) {
				return true
			}
		}
	}

	return false
}
