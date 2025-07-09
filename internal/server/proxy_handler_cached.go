package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/connectors"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// CachedProxyHandler wraps ProxyHandler with caching capabilities
type CachedProxyHandler struct {
	*ProxyHandler
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

// NewCachedProxyHandler creates a new cached proxy handler
func NewCachedProxyHandler(proxyHandler *ProxyHandler, c cache.Cache, config CacheConfig) *CachedProxyHandler {
	return &CachedProxyHandler{
		ProxyHandler: proxyHandler,
		cache:        c,
		keyGen:       cache.NewKeyGenerator("starport"),
		cacheConfig:  config,
	}
}

// RegisterRoutes adds cached proxy routes to the router
func (h *CachedProxyHandler) RegisterRoutes(r chi.Router) {
	// OpenAI-compatible endpoints
	r.Post("/v1/chat/completions", h.handleCachedChatCompletions)
	r.Post("/v1/embeddings", h.handleCachedEmbeddings)
	r.Get("/v1/models", h.handleCachedModels)
}

// RegisterOpenRouterRoutes adds cached OpenRouter-compatible routes
func (h *CachedProxyHandler) RegisterOpenRouterRoutes(r chi.Router) {
	r.Post("/chat/completions", h.handleCachedChatCompletions)
	r.Post("/embeddings", h.handleCachedEmbeddings)
	r.Get("/models", h.handleCachedModels)
	r.Get("/models/{model}/endpoints", h.handleModelEndpoints) // No caching needed
	r.Get("/providers", h.handleCachedProviders)
}

// handleCachedChatCompletions handles chat completions with caching
func (h *CachedProxyHandler) handleCachedChatCompletions(w http.ResponseWriter, r *http.Request) {
	if !h.cacheConfig.EnableChatCache {
		h.handleChatCompletions(w, r)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Failed to read request body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Parse request to generate cache key
	var req connectors.ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body: "+err.Error())
		return
	}

	// Check if caching should be skipped
	if h.shouldSkipCache(r, req.Model) || req.Stream {
		// Skip cache for streaming requests
		h.handleChatCompletions(w, r)
		return
	}

	// Convert to cache request type
	cacheReq := h.toCacheChatRequest(&req)
	
	// Generate cache key
	cacheKey := h.keyGen.ChatCompletionKey(cacheReq)

	// Try to get from cache
	ctx := r.Context()
	if cachedData, found, err := h.cache.Get(ctx, cacheKey); found && err == nil {
		// Cache hit
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		if _, err := w.Write(cachedData); err != nil {
			log.Error().Err(err).Msg("failed to write cached response")
		}
		
		log.Debug().
			Str("cache_key", cacheKey).
			Str("model", req.Model).
			Msg("cache hit for chat completion")
		return
	}

	// Cache miss - create response recorder to capture response
	recorder := &responseRecorder{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}

	// Add cache miss header
	recorder.Header().Set("X-Cache", "MISS")

	// Call the underlying handler
	h.handleChatCompletions(recorder, r)

	// Cache successful responses only
	if recorder.statusCode == http.StatusOK && recorder.body.Len() > 0 {
		policy := cache.DefaultPolicies()[cache.PolicyTypeChatCompletion]
		if err := h.cache.Set(ctx, cacheKey, recorder.body.Bytes(), policy.TTL); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache chat completion response")
		}
	}
}

// handleCachedEmbeddings handles embeddings with caching
func (h *CachedProxyHandler) handleCachedEmbeddings(w http.ResponseWriter, r *http.Request) {
	if !h.cacheConfig.EnableEmbeddingCache {
		h.handleEmbeddings(w, r)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Failed to read request body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Parse request to generate cache key
	var req connectors.EmbeddingsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body: "+err.Error())
		return
	}

	// Check if caching should be skipped
	if h.shouldSkipCache(r, req.Model) {
		h.handleEmbeddings(w, r)
		return
	}

	// Convert to cache request type
	cacheReq := h.toCacheEmbeddingRequest(&req)
	
	// Generate cache key
	cacheKey := h.keyGen.EmbeddingKey(cacheReq)

	// Try to get from cache
	ctx := r.Context()
	if cachedData, found, err := h.cache.Get(ctx, cacheKey); found && err == nil {
		// Cache hit
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		if _, err := w.Write(cachedData); err != nil {
			log.Error().Err(err).Msg("failed to write cached response")
		}
		
		log.Debug().
			Str("cache_key", cacheKey).
			Str("model", req.Model).
			Msg("cache hit for embedding")
		return
	}

	// Cache miss - create response recorder
	recorder := &responseRecorder{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}

	recorder.Header().Set("X-Cache", "MISS")

	// Call the underlying handler
	h.handleEmbeddings(recorder, r)

	// Cache successful responses
	if recorder.statusCode == http.StatusOK && recorder.body.Len() > 0 {
		policy := cache.DefaultPolicies()[cache.PolicyTypeEmbedding]
		if err := h.cache.Set(ctx, cacheKey, recorder.body.Bytes(), policy.TTL); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache embedding response")
		}
	}
}

// handleCachedModels handles model listing with caching
func (h *CachedProxyHandler) handleCachedModels(w http.ResponseWriter, r *http.Request) {
	if !h.cacheConfig.EnableModelCache {
		h.handleModels(w, r)
		return
	}

	// Generate cache key based on path (different for /v1 vs /api/v1)
	cacheKey := h.keyGen.ModelListKey("")
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		cacheKey = h.keyGen.ModelListKey("enhanced")
	}

	// Check cache control header
	if h.shouldSkipCache(r, "") {
		h.handleModels(w, r)
		return
	}

	// Try to get from cache
	ctx := r.Context()
	if cachedData, found, err := h.cache.Get(ctx, cacheKey); found && err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		if _, err := w.Write(cachedData); err != nil {
			log.Error().Err(err).Msg("failed to write cached response")
		}
		return
	}

	// Cache miss - create response recorder
	recorder := &responseRecorder{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}

	recorder.Header().Set("X-Cache", "MISS")

	// Call the underlying handler
	h.handleModels(recorder, r)

	// Cache successful responses
	if recorder.statusCode == http.StatusOK && recorder.body.Len() > 0 {
		policy := cache.DefaultPolicies()[cache.PolicyTypeModel]
		if err := h.cache.Set(ctx, cacheKey, recorder.body.Bytes(), policy.TTL); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache models response")
		}
	}
}

// handleCachedProviders handles provider listing with caching
func (h *CachedProxyHandler) handleCachedProviders(w http.ResponseWriter, r *http.Request) {
	if !h.cacheConfig.EnableProviderCache {
		h.handleProviders(w, r)
		return
	}

	cacheKey := h.keyGen.ProviderListKey()

	// Check cache control header
	if h.shouldSkipCache(r, "") {
		h.handleProviders(w, r)
		return
	}

	// Try to get from cache
	ctx := r.Context()
	if cachedData, found, err := h.cache.Get(ctx, cacheKey); found && err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		if _, err := w.Write(cachedData); err != nil {
			log.Error().Err(err).Msg("failed to write cached response")
		}
		return
	}

	// Cache miss - create response recorder
	recorder := &responseRecorder{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}

	recorder.Header().Set("X-Cache", "MISS")

	// Call the underlying handler
	h.handleProviders(recorder, r)

	// Cache successful responses
	if recorder.statusCode == http.StatusOK && recorder.body.Len() > 0 {
		policy := cache.DefaultPolicies()[cache.PolicyTypeProvider]
		if err := h.cache.Set(ctx, cacheKey, recorder.body.Bytes(), policy.TTL); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache providers response")
		}
	}
}

// shouldSkipCache checks if caching should be skipped for this request
func (h *CachedProxyHandler) shouldSkipCache(r *http.Request, model string) bool {
	// Check cache control header
	if r.Header.Get(h.cacheConfig.CacheControlHeader) == "no-cache" {
		return true
	}

	// Check if model is in skip list
	if model != "" {
		for _, skipModel := range h.cacheConfig.SkipCacheModels {
			if strings.Contains(model, skipModel) {
				return true
			}
		}
	}

	return false
}

// toCacheChatRequest converts connector request to cache request
func (h *CachedProxyHandler) toCacheChatRequest(req *connectors.ChatRequest) cache.ChatCompletionRequest {
	return ConvertToCacheChatRequest(req)
}

// toCacheEmbeddingRequest converts connector request to cache request
func (h *CachedProxyHandler) toCacheEmbeddingRequest(req *connectors.EmbeddingsRequest) cache.EmbeddingRequest {
	// Convert strings to pointers
	var encodingFormat *string
	if req.EncodingFormat != "" {
		encodingFormat = &req.EncodingFormat
	}
	
	var user *string
	if req.User != "" {
		user = &req.User
	}

	return cache.EmbeddingRequest{
		Model:          req.Model,
		Input:          req.Input,
		EncodingFormat: encodingFormat,
		Dimensions:     req.Dimensions,
		User:           user,
	}
}

// responseRecorder captures the response for caching
type responseRecorder struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	r.body.Write(data)
	return r.ResponseWriter.Write(data)
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}