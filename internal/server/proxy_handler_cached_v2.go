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

// CachedProxyHandlerV2 wraps ProxyHandler with the new cache Manager
type CachedProxyHandlerV2 struct {
	*ProxyHandler
	cacheManager *cache.Manager
	cacheConfig  CacheConfig
}

// NewCachedProxyHandlerV2 creates a new cached proxy handler with cache Manager
func NewCachedProxyHandlerV2(proxyHandler *ProxyHandler, cm *cache.Manager, config CacheConfig) *CachedProxyHandlerV2 {
	return &CachedProxyHandlerV2{
		ProxyHandler: proxyHandler,
		cacheManager: cm,
		cacheConfig:  config,
	}
}

// RegisterRoutes adds cached proxy routes to the router
func (h *CachedProxyHandlerV2) RegisterRoutes(r chi.Router) {
	// OpenAI-compatible endpoints
	r.Post("/v1/chat/completions", h.handleCachedChatCompletions)
	r.Post("/v1/embeddings", h.handleCachedEmbeddings)
	r.Get("/v1/models", h.handleCachedModels)
}

// RegisterOpenRouterRoutes adds cached OpenRouter-compatible routes
func (h *CachedProxyHandlerV2) RegisterOpenRouterRoutes(r chi.Router) {
	r.Post("/chat/completions", h.handleCachedChatCompletions)
	r.Post("/embeddings", h.handleCachedEmbeddings)
	r.Get("/models", h.handleCachedModels)
	r.Get("/models/{model}/endpoints", h.handleModelEndpoints) // No caching needed
	r.Get("/providers", h.handleCachedProviders)
}

// handleCachedChatCompletions handles chat completions with caching
func (h *CachedProxyHandlerV2) handleCachedChatCompletions(w http.ResponseWriter, r *http.Request) {
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
	keyGen := h.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.ChatCompletionKey(cacheReq)

	// Try to get from cache
	ctx := r.Context()
	if cachedData, found, err := h.cacheManager.GetResponse(ctx, cacheKey); found && err == nil {
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
		if err := h.cacheManager.SetResponse(ctx, cacheKey, recorder.body.Bytes()); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache chat completion response")
		}
	}
}

// handleCachedEmbeddings handles embeddings with caching
func (h *CachedProxyHandlerV2) handleCachedEmbeddings(w http.ResponseWriter, r *http.Request) {
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
	keyGen := h.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.EmbeddingKey(cacheReq)

	// Try to get from cache
	ctx := r.Context()
	if cachedData, found, err := h.cacheManager.GetResponse(ctx, cacheKey); found && err == nil {
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
		if err := h.cacheManager.SetResponse(ctx, cacheKey, recorder.body.Bytes()); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", cacheKey).
				Msg("failed to cache embedding response")
		}
	}
}

// handleCachedModels handles model listing with caching
func (h *CachedProxyHandlerV2) handleCachedModels(w http.ResponseWriter, r *http.Request) {
	if !h.cacheConfig.EnableModelCache {
		h.handleModels(w, r)
		return
	}

	// Generate cache key based on path (different for /v1 vs /api/v1)
	keyGen := h.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.ModelListKey("")
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		cacheKey = keyGen.ModelListKey("enhanced")
	}

	// Check cache control header
	if h.shouldSkipCache(r, "") {
		h.handleModels(w, r)
		return
	}

	// Try to get from cache (model metadata uses local cache)
	ctx := r.Context()
	if cachedData, found, err := h.cacheManager.GetModel(ctx, cacheKey); found && err == nil {
		// Cache hit - convert back to bytes
		jsonData, _ := json.Marshal(cachedData)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		if _, err := w.Write(jsonData); err != nil {
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
		// Parse the response to cache as structured data
		var modelData interface{}
		if err := json.Unmarshal(recorder.body.Bytes(), &modelData); err == nil {
			if err := h.cacheManager.SetModel(ctx, cacheKey, modelData); err != nil {
				log.Warn().
					Err(err).
					Str("cache_key", cacheKey).
					Msg("failed to cache models response")
			}
		}
	}
}

// handleCachedProviders handles provider listing with caching
func (h *CachedProxyHandlerV2) handleCachedProviders(w http.ResponseWriter, r *http.Request) {
	if !h.cacheConfig.EnableProviderCache {
		h.handleProviders(w, r)
		return
	}

	keyGen := h.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.ProviderListKey()

	// Check cache control header
	if h.shouldSkipCache(r, "") {
		h.handleProviders(w, r)
		return
	}

	// Try to get from cache (provider metadata uses local cache)
	ctx := r.Context()
	if cachedData, found, err := h.cacheManager.GetModel(ctx, cacheKey); found && err == nil {
		// Cache hit - convert back to bytes
		jsonData, _ := json.Marshal(cachedData)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		if _, err := w.Write(jsonData); err != nil {
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
		// Parse the response to cache as structured data
		var providerData interface{}
		if err := json.Unmarshal(recorder.body.Bytes(), &providerData); err == nil {
			if err := h.cacheManager.SetModel(ctx, cacheKey, providerData); err != nil {
				log.Warn().
					Err(err).
					Str("cache_key", cacheKey).
					Msg("failed to cache providers response")
			}
		}
	}
}

// shouldSkipCache checks if caching should be skipped for this request
func (h *CachedProxyHandlerV2) shouldSkipCache(r *http.Request, model string) bool {
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
func (h *CachedProxyHandlerV2) toCacheChatRequest(req *connectors.ChatRequest) cache.ChatCompletionRequest {
	return ConvertToCacheChatRequest(req)
}

// toCacheEmbeddingRequest converts connector request to cache request
func (h *CachedProxyHandlerV2) toCacheEmbeddingRequest(req *connectors.EmbeddingsRequest) cache.EmbeddingRequest {
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