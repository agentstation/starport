package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/rs/zerolog/log"
)

// Define typed context keys
type contextKey string

const (
	// CacheStatusKey is the context key for cache status
	CacheStatusKey contextKey = "X-Cache"

	// CacheStatusHit indicates a cache hit
	CacheStatusHit = "HIT"
	// CacheStatusMiss indicates a cache miss
	CacheStatusMiss = "MISS"
)

// CachedService wraps a Service with cache Manager
type CachedService struct {
	service      Service
	cacheManager *cache.Manager
	cacheConfig  CacheConfig
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

// NewCachedService creates a new cached service with cache Manager
func NewCachedService(service Service, cm *cache.Manager, config CacheConfig) Service {
	return &CachedService{
		service:      service,
		cacheManager: cm,
		cacheConfig:  config,
	}
}

// ProcessChatCompletion handles chat completions with Manager-based caching
func (s *CachedService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Skip cache for streaming requests or if caching is disabled
	if req.Stream || !s.cacheConfig.EnableChatCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessChatCompletion(ctx, req)
	}

	// Use cache manager for chat completions
	cacheReq := toCacheChatRequest(req)
	keyGen := s.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.ChatCompletionKey(cacheReq)

	// Try to get from cache
	cachedResp, err := s.cacheManager.GetChatCompletion(ctx, cacheKey)
	if err == nil && cachedResp != nil {
		log.Debug().
			Str("model", req.Model).
			Msg("cache hit for chat completion")
		resp := fromCacheChatResponse(cachedResp)
		resp.CacheStatus = CacheStatusHit
		return resp, nil
	}

	// Cache miss - call underlying service
	resp, err := s.service.ProcessChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.CacheStatus = CacheStatusMiss

	// Cache successful responses
	cacheResp := toCacheChatResponse(resp)
	if err := s.cacheManager.SetChatCompletion(ctx, cacheKey, cacheResp); err != nil {
		log.Warn().
			Err(err).
			Str("model", req.Model).
			Msg("failed to cache chat completion response")
	}

	return resp, nil
}

// ProcessChatCompletionStream handles streaming requests with caching
func (s *CachedService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	// Skip cache if disabled or should skip
	if !s.cacheConfig.EnableChatCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessChatCompletionStream(ctx, req)
	}

	// Use same cache key as non-streaming requests
	cacheReq := toCacheChatRequest(req)
	keyGen := s.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.ChatCompletionKey(cacheReq)

	// Try to get from cache
	cachedResp, err := s.cacheManager.GetChatCompletion(ctx, cacheKey)
	if err == nil && cachedResp != nil {
		log.Debug().
			Str("model", req.Model).
			Msg("cache hit for streaming chat completion")
		
		// Convert cached response to streaming response
		return newCachedStreamResponse(cachedResp, req.Model, CacheStatusHit), nil
	}

	// Cache miss - get stream from service
	stream, err := s.service.ProcessChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	// Wrap stream to cache the response
	return newCachingStreamWrapper(ctx, stream, s.cacheManager, cacheKey, req.Model), nil
}

// ProcessEmbeddings handles embeddings with caching
func (s *CachedService) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Skip cache if disabled
	if !s.cacheConfig.EnableEmbeddingCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessEmbeddings(ctx, req)
	}

	// Convert to cache request type
	cacheReq := toCacheEmbeddingRequest(req)

	// Use cache manager for embeddings
	keyGen := s.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.EmbeddingKey(cacheReq)

	// Try to get from cache
	cachedResp, err := s.cacheManager.GetEmbedding(ctx, cacheKey)
	if err == nil && cachedResp != nil {
		log.Debug().
			Str("model", req.Model).
			Msg("cache hit for embedding")
		resp := fromCacheEmbeddingsResponse(cachedResp)
		resp.CacheStatus = CacheStatusHit
		return resp, nil
	}

	// Cache miss
	resp, err := s.service.ProcessEmbeddings(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.CacheStatus = CacheStatusMiss

	// Cache successful responses
	cacheResp := toCacheEmbeddingsResponse(resp)
	if err := s.cacheManager.SetEmbedding(ctx, cacheKey, cacheResp); err != nil {
		log.Warn().
			Err(err).
			Str("model", req.Model).
			Msg("failed to cache embedding response")
	}

	return resp, nil
}

// ListModels returns available models with caching
func (s *CachedService) ListModels(ctx context.Context) (*ModelsResponse, error) {
	if !s.cacheConfig.EnableModelCache || s.shouldSkipCache(ctx, "") {
		return s.service.ListModels(ctx)
	}

	// Generate cache key
	keyGen := s.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.ModelListKey("")

	// Try to get from cache using the responses cache
	cachedData, found, err := s.cacheManager.GetResponse(ctx, cacheKey)
	if err == nil && found {
		var resp ModelsResponse
		if err := json.Unmarshal(cachedData, &resp); err == nil {
			log.Debug().Msg("cache hit for models list")
			resp.CacheStatus = CacheStatusHit
			return &resp, nil
		}
	}

	// Cache miss
	resp, err := s.service.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	resp.CacheStatus = CacheStatusMiss

	// Cache successful responses
	if respData, err := json.Marshal(resp); err == nil {
		if err := s.cacheManager.SetResponse(ctx, cacheKey, respData); err != nil {
			log.Warn().
				Err(err).
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

	// Generate cache key
	keyGen := s.cacheManager.GetKeyGenerator()
	cacheKey := keyGen.ProviderListKey()

	// Try to get from cache using the responses cache
	cachedData, found, err := s.cacheManager.GetResponse(ctx, cacheKey)
	if err == nil && found {
		var resp ProvidersResponse
		if err := json.Unmarshal(cachedData, &resp); err == nil {
			log.Debug().Msg("cache hit for providers list")
			resp.CacheStatus = CacheStatusHit
			return &resp, nil
		}
	}

	// Cache miss
	resp, err := s.service.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	resp.CacheStatus = CacheStatusMiss

	// Cache successful responses
	if respData, err := json.Marshal(resp); err == nil {
		if err := s.cacheManager.SetResponse(ctx, cacheKey, respData); err != nil {
			log.Warn().
				Err(err).
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

// fromCacheChatResponse converts cache response to proxy response
func fromCacheChatResponse(cached *cache.ChatCompletionResponse) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		ID:                cached.ID,
		Object:            cached.Object,
		Created:           cached.Created,
		Model:             cached.Model,
		Choices:           convertToConnectorChoices(cached.Choices),
		Usage:             convertToConnectorUsage(cached.Usage),
		SystemFingerprint: cached.SystemFingerprint,
		ModelUsed:         cached.ModelUsed,
	}
}

// fromCacheEmbeddingsResponse converts cache response to proxy response
func fromCacheEmbeddingsResponse(cached *cache.EmbeddingsResponse) *EmbeddingsResponse {
	return &EmbeddingsResponse{
		Object: cached.Object,
		Data:   convertToConnectorEmbeddings(cached.Data),
		Model:  cached.Model,
		Usage:  convertToConnectorUsage(cached.Usage),
	}
}

// toCacheChatResponse converts proxy response to cache response
func toCacheChatResponse(resp *ChatCompletionResponse) *cache.ChatCompletionResponse {
	return &cache.ChatCompletionResponse{
		ID:                resp.ID,
		Object:            resp.Object,
		Created:           resp.Created,
		Model:             resp.Model,
		Choices:           resp.Choices,
		Usage:             resp.Usage,
		SystemFingerprint: resp.SystemFingerprint,
		ModelUsed:         resp.ModelUsed,
		// Note: CacheStatus is not cached
	}
}

// toCacheEmbeddingsResponse converts proxy response to cache response
func toCacheEmbeddingsResponse(resp *EmbeddingsResponse) *cache.EmbeddingsResponse {
	return &cache.EmbeddingsResponse{
		Object: resp.Object,
		Data:   resp.Data,
		Model:  resp.Model,
		Usage:  resp.Usage,
		// Note: CacheStatus is not cached
	}
}

// Helper functions for type conversions
func convertToConnectorChoices(data interface{}) []connectors.Choice {
	// If it's already the right type, return it
	if choices, ok := data.([]connectors.Choice); ok {
		return choices
	}
	// Otherwise, we need to unmarshal and re-marshal
	// For now, return empty slice to avoid panics
	return []connectors.Choice{}
}

func convertToConnectorEmbeddings(data interface{}) []connectors.Embedding {
	// If it's already the right type, return it
	if embeddings, ok := data.([]connectors.Embedding); ok {
		return embeddings
	}
	// Otherwise, we need to unmarshal and re-marshal
	// For now, return empty slice to avoid panics
	return []connectors.Embedding{}
}

func convertToConnectorUsage(data interface{}) *connectors.Usage {
	// If it's already the right type, return it
	if usage, ok := data.(*connectors.Usage); ok {
		return usage
	}
	// Otherwise return nil
	return nil
}

// cachingStreamWrapper wraps a stream to cache the complete response
type cachingStreamWrapper struct {
	ctx          context.Context
	stream       ChatCompletionStreamResponse
	cacheManager *cache.Manager
	cacheKey     string
	modelID      string
	chunks       []connectors.ChatStreamChunk
	cacheStatus  string
}

func newCachingStreamWrapper(ctx context.Context, stream ChatCompletionStreamResponse, cm *cache.Manager, cacheKey, modelID string) *cachingStreamWrapper {
	return &cachingStreamWrapper{
		ctx:          ctx,
		stream:       stream,
		cacheManager: cm,
		cacheKey:     cacheKey,
		modelID:      modelID,
		chunks:       make([]connectors.ChatStreamChunk, 0),
		cacheStatus:  CacheStatusMiss,
	}
}

func (w *cachingStreamWrapper) Read() (*connectors.ChatStreamChunk, error) {
	chunk, err := w.stream.Read()
	if err != nil {
		if err == io.EOF {
			// Stream completed, cache the accumulated response
			go w.cacheResponse()
		}
		return chunk, err
	}

	// Accumulate chunks
	if chunk != nil {
		w.chunks = append(w.chunks, *chunk)
	}

	return chunk, nil
}

func (w *cachingStreamWrapper) Close() error {
	return w.stream.Close()
}

func (w *cachingStreamWrapper) GetCacheStatus() string {
	return w.cacheStatus
}

func (w *cachingStreamWrapper) cacheResponse() {
	// Reconstruct the complete response from chunks
	response := w.reconstructResponse()
	if response == nil {
		return
	}

	// Convert to cache response
	cacheResp := toCacheChatResponse(response)
	
	// Cache the response
	if err := w.cacheManager.SetChatCompletion(w.ctx, w.cacheKey, cacheResp); err != nil {
		log.Warn().
			Err(err).
			Str("model", w.modelID).
			Msg("failed to cache streaming response")
	} else {
		log.Debug().
			Str("model", w.modelID).
			Msg("cached streaming response")
	}
}

func (w *cachingStreamWrapper) reconstructResponse() *ChatCompletionResponse {
	if len(w.chunks) == 0 {
		return nil
	}

	// Find the last chunk with usage data
	var usage *connectors.Usage
	for i := len(w.chunks) - 1; i >= 0; i-- {
		if w.chunks[i].Usage != nil {
			usage = w.chunks[i].Usage
			break
		}
	}

	// Accumulate content and reasoning
	var content, reasoning, finishReason string
	for _, chunk := range w.chunks {
		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta.Content != "" {
				content += choice.Delta.Content
			}
			if choice.Delta.Reasoning != "" {
				reasoning += choice.Delta.Reasoning
			}
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
	}

	// Create response
	return &ChatCompletionResponse{
		ID:      w.chunks[0].ID,
		Object:  "chat.completion",
		Created: w.chunks[0].Created,
		Model:   w.modelID,
		Choices: []connectors.Choice{
			{
				Index: 0,
				Message: connectors.Message{
					Role:      "assistant",
					Content:   content,
					Reasoning: reasoning,
				},
				FinishReason: finishReason,
			},
		},
		Usage: usage,
	}
}

// cachedStreamResponse creates a stream from a cached response
type cachedStreamResponse struct {
	response    *cache.ChatCompletionResponse
	modelID     string
	cacheStatus string
	position    int
	chunkSize   int
}

func newCachedStreamResponse(response *cache.ChatCompletionResponse, modelID, cacheStatus string) *cachedStreamResponse {
	return &cachedStreamResponse{
		response:    response,
		modelID:     modelID,
		cacheStatus: cacheStatus,
		position:    0,
		chunkSize:   20, // Emit chunks of roughly 20 characters
	}
}

func (r *cachedStreamResponse) Read() (*connectors.ChatStreamChunk, error) {
	// Convert choices from interface{} to actual choices
	choices := convertToConnectorChoices(r.response.Choices)
	if len(choices) == 0 {
		return nil, io.EOF
	}
	
	choice := choices[0]
	content, _ := choice.Message.Content.(string)
	reasoning := choice.Message.Reasoning

	// First chunk with role
	if r.position == 0 {
		chunk := &connectors.ChatStreamChunk{
			ID:      r.response.ID,
			Object:  "chat.completion.chunk",
			Created: r.response.Created,
			Model:   r.modelID,
			Choices: []connectors.StreamChoice{
				{
					Index: 0,
					Delta: connectors.MessageDelta{
						Role: "assistant",
					},
				},
			},
		}
		r.position = 1
		return chunk, nil
	}

	// Calculate content position (position-1 because first chunk was role)
	contentPos := (r.position - 1) * r.chunkSize
	
	// Send reasoning first if present and not yet sent
	if reasoning != "" && contentPos == 0 {
		chunk := &connectors.ChatStreamChunk{
			ID:      r.response.ID,
			Object:  "chat.completion.chunk",
			Created: r.response.Created,
			Model:   r.modelID,
			Choices: []connectors.StreamChoice{
				{
					Index: 0,
					Delta: connectors.MessageDelta{
						Reasoning: reasoning,
					},
				},
			},
		}
		r.position++
		return chunk, nil
	}

	// Adjust content position if we sent reasoning
	if reasoning != "" && contentPos > 0 {
		contentPos -= r.chunkSize
	}

	// Send content in chunks
	if contentPos < len(content) {
		endPos := contentPos + r.chunkSize
		if endPos > len(content) {
			endPos = len(content)
		}

		chunk := &connectors.ChatStreamChunk{
			ID:      r.response.ID,
			Object:  "chat.completion.chunk",
			Created: r.response.Created,
			Model:   r.modelID,
			Choices: []connectors.StreamChoice{
				{
					Index: 0,
					Delta: connectors.MessageDelta{
						Content: content[contentPos:endPos],
					},
				},
			},
		}

		// If this is the last content chunk, add finish reason and usage
		if endPos >= len(content) {
			chunk.Choices[0].FinishReason = choice.FinishReason
			chunk.Usage = convertToConnectorUsage(r.response.Usage)
		}

		r.position++
		return chunk, nil
	}

	return nil, io.EOF
}

func (r *cachedStreamResponse) Close() error {
	// Nothing to close for cached response
	return nil
}

func (r *cachedStreamResponse) GetCacheStatus() string {
	return r.cacheStatus
}

// Add interface to check cache status on streams
type CacheStatusProvider interface {
	GetCacheStatus() string
}

