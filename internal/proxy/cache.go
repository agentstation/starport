package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/pkg/catalog"
	"github.com/rs/zerolog/log"
)

// CacheManager interface defines the cache operations used by the proxy
type CacheManager interface {
	GetModel(ctx context.Context, key string) (interface{}, bool, error)
	SetModel(ctx context.Context, key string, value interface{}) error
	GetChatCompletion(ctx context.Context, key string) (*cache.ChatCompletionResponse, error)
	SetChatCompletion(ctx context.Context, key string, response *cache.ChatCompletionResponse) error
	GetEmbedding(ctx context.Context, key string) (*cache.EmbeddingsResponse, error)
	SetEmbedding(ctx context.Context, key string, response *cache.EmbeddingsResponse) error
}

// CacheMiddleware provides caching functionality for proxy services.
type CacheMiddleware struct {
	manager CacheManager
	config  *CacheConfig
}

// NewCacheMiddleware creates a new cache middleware.
func NewCacheMiddleware(manager CacheManager, config *CacheConfig) Middleware {
	return &CacheMiddleware{
		manager: manager,
		config:  config,
	}
}

// Wrap wraps the service with caching functionality.
func (m *CacheMiddleware) Wrap(next Proxy) Proxy {
	return &cachedService{
		service:      next,
		cacheManager: m.manager,
		cacheConfig:  *m.config,
	}
}

// cachedService implements the Proxy interface with caching
type cachedService struct {
	service      Proxy
	cacheManager CacheManager
	cacheConfig  CacheConfig
}

// ProcessChatCompletion handles chat completions with Manager-based caching
func (s *cachedService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	log.Info().
		Str("model", req.Model).
		Bool("stream", req.Stream).
		Bool("cache_enabled", s.cacheConfig.EnableChatCache).
		Msg("CachedService.ProcessChatCompletion called")
	
	// Skip cache for streaming requests or if caching is disabled
	if req.Stream || !s.cacheConfig.EnableChatCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessChatCompletion(ctx, req)
	}

	// Generate cache key
	cacheKey, err := s.generateChatCacheKey(req)
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate cache key")
		return s.service.ProcessChatCompletion(ctx, req)
	}

	// Try to get from cache using the cache.Manager's GetChatCompletion method
	cachedResp, err := s.cacheManager.GetChatCompletion(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Str("key", cacheKey).Msg("cache get error")
		// Continue without cache on error
		return s.service.ProcessChatCompletion(ctx, req)
	}

	if cachedResp != nil {
		log.Info().
			Str("model", req.Model).
			Str("cache_key", cacheKey).
			Msg("cache hit for chat completion")
		
		// Calculate cache age
		cacheAge := 0
		if cachedResp.CachedAt > 0 {
			cacheAge = int(time.Now().Unix() - cachedResp.CachedAt)
		}
		
		// Convert cache.ChatCompletionResponse to proxy.ChatCompletionResponse
		resp := &ChatCompletionResponse{
			ID:                cachedResp.ID,
			Object:            cachedResp.Object,
			Created:           cachedResp.Created,
			Model:             cachedResp.Model,
			SystemFingerprint: cachedResp.SystemFingerprint,
			ModelUsed:         cachedResp.ModelUsed,
			CacheStatus:       CacheStatusHit,
			CacheAge:          cacheAge,
		}
		
		// Copy choices and usage from cached response with all fields
		resp.Choices = make([]connectors.Choice, len(cachedResp.Choices))
		for i, choice := range cachedResp.Choices {
			resp.Choices[i] = connectors.Choice{
				Index: choice.Index,
				Message: connectors.Message{
					Role:       choice.Message.Role,
					Content:    choice.Message.Content,
					Reasoning:  choice.Message.Reasoning,
					Name:       choice.Message.Name,
					ToolCalls:  choice.Message.ToolCalls,
					ToolCallID: choice.Message.ToolCallID,
				},
				FinishReason: choice.FinishReason,
				LogProbs:     choice.LogProbs,
			}
		}
		resp.Usage = cachedResp.Usage
		
		return resp, nil
	}

	log.Info().
		Str("model", req.Model).
		Str("cache_key", cacheKey).
		Msg("cache miss for chat completion")

	// Execute the request
	resp, err := s.service.ProcessChatCompletion(ctx, req)
	if err != nil {
		// Check if this is a 404 model not found error
		if provErr, ok := err.(*ProviderError); ok && provErr.Err != nil {
			if apiErr, ok := provErr.Err.(*connectors.APIError); ok && apiErr.StatusCode == 404 {
				// Mark the model as invalid
				catalog.MarkModelInvalid(req.Model)
				log.Warn().
					Str("model", req.Model).
					Msg("marking model as invalid due to 404 error in cached non-streaming")
			}
		}
		return nil, err
	}

	// Mark as cache miss
	resp.CacheStatus = CacheStatusMiss

	// Cache the response (async to not block)
	go func() {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		// Convert proxy response to cache response
		cacheResp := &cache.ChatCompletionResponse{
			ID:                resp.ID,
			Object:            resp.Object,
			Created:           resp.Created,
			Model:             resp.Model,
			Choices:           resp.Choices,
			Usage:             resp.Usage,
			SystemFingerprint: resp.SystemFingerprint,
			ModelUsed:         resp.ModelUsed,
			CachedAt:          time.Now().Unix(),
		}
		
		if err := s.cacheManager.SetChatCompletion(cacheCtx, cacheKey, cacheResp); err != nil {
			log.Warn().Err(err).Str("key", cacheKey).Msg("failed to cache response")
		}
	}()

	return resp, nil
}

// ProcessChatCompletionStream handles streaming requests with caching
func (s *cachedService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	log.Info().
		Str("model", req.Model).
		Bool("cache_enabled", s.cacheConfig.EnableChatCache).
		Msg("CachedService.ProcessChatCompletionStream called")

	// Skip cache if disabled or should skip
	if !s.cacheConfig.EnableChatCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessChatCompletionStream(ctx, req)
	}

	// Generate cache key - same as non-streaming
	cacheKey, err := s.generateChatCacheKey(req)
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate cache key for streaming")
		return s.service.ProcessChatCompletionStream(ctx, req)
	}

	// Try to get from cache
	cachedResp, err := s.cacheManager.GetChatCompletion(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Str("key", cacheKey).Msg("cache get error for streaming")
		// Continue without cache on error
		stream, err := s.service.ProcessChatCompletionStream(ctx, req)
		if err != nil {
			// Check if this is a 404 model not found error
			if provErr, ok := err.(*ProviderError); ok && provErr.Err != nil {
				if apiErr, ok := provErr.Err.(*connectors.APIError); ok && apiErr.StatusCode == 404 {
					// Mark the model as invalid
					catalog.MarkModelInvalid(req.Model)
					log.Warn().
						Str("model", req.Model).
						Msg("marking model as invalid due to 404 error in cached streaming (cache error path)")
				}
			}
			return nil, err
		}
		return newCachingStreamWrapper(ctx, stream, s.cacheManager, cacheKey, req.Model), nil
	}

	if cachedResp != nil {
		log.Info().
			Str("model", req.Model).
			Str("cache_key", cacheKey).
			Msg("cache hit for streaming chat completion")
		
		// Convert cached response to streaming response
		return newCachedStreamResponse(cachedResp, req.Model, CacheStatusHit), nil
	}

	log.Info().
		Str("model", req.Model).
		Str("cache_key", cacheKey).
		Msg("cache miss for streaming chat completion")

	// Cache miss - get stream from service
	stream, err := s.service.ProcessChatCompletionStream(ctx, req)
	if err != nil {
		// Check if this is a 404 model not found error
		if provErr, ok := err.(*ProviderError); ok && provErr.Err != nil {
			if apiErr, ok := provErr.Err.(*connectors.APIError); ok && apiErr.StatusCode == 404 {
				// Mark the model as invalid
				catalog.MarkModelInvalid(req.Model)
				log.Warn().
					Str("model", req.Model).
					Msg("marking model as invalid due to 404 error in cached streaming")
			}
		}
		return nil, err
	}

	// Wrap stream to cache the response
	return newCachingStreamWrapper(ctx, stream, s.cacheManager, cacheKey, req.Model), nil
}

// ProcessEmbeddings handles embeddings with caching
func (s *cachedService) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Skip cache if disabled
	if !s.cacheConfig.EnableEmbeddingCache || s.shouldSkipCache(ctx, req.Model) {
		return s.service.ProcessEmbeddings(ctx, req)
	}

	// Generate cache key
	cacheKey, err := s.generateEmbeddingsCacheKey(req)
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate embeddings cache key")
		return s.service.ProcessEmbeddings(ctx, req)
	}

	// Try to get from cache
	cachedResp, err := s.cacheManager.GetEmbedding(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Str("key", cacheKey).Msg("embeddings cache get error")
		return s.service.ProcessEmbeddings(ctx, req)
	}

	if cachedResp != nil {
		log.Debug().
			Str("model", req.Model).
			Msg("cache hit for embedding")
		
		// Convert cache.EmbeddingsResponse to proxy.EmbeddingsResponse
		resp := &EmbeddingsResponse{
			Object:      cachedResp.Object,
			Model:       cachedResp.Model,
			CacheStatus: CacheStatusHit,
			CacheAge:    0, // TODO: Calculate actual cache age
		}
		
		// Copy data and usage from cached response
		resp.Data = cachedResp.Data
		resp.Usage = cachedResp.Usage
		return resp, nil
	}

	// Execute the request
	resp, err := s.service.ProcessEmbeddings(ctx, req)
	if err != nil {
		return nil, err
	}

	resp.CacheStatus = CacheStatusMiss

	// Cache the response
	go func() {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		// Convert proxy response to cache response
		cacheResp := &cache.EmbeddingsResponse{
			Object: resp.Object,
			Data:   resp.Data,
			Model:  resp.Model,
			Usage:  resp.Usage,
		}
		
		if err := s.cacheManager.SetEmbedding(cacheCtx, cacheKey, cacheResp); err != nil {
			log.Warn().Err(err).Str("key", cacheKey).Msg("failed to cache embeddings")
		}
	}()

	return resp, nil
}

// cacheListResponse is a helper for caching list responses (models, providers)
func (s *cachedService) cacheListResponse(ctx context.Context, cacheKey, cacheMsg string, fetchFunc func() (interface{}, error)) (interface{}, error) {
	// Try to get from cache
	cached, found, err := s.cacheManager.GetModel(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Msgf("%s cache get error", cacheMsg)
		return fetchFunc()
	}

	if found {
		// The cache returns a generic map, we need to convert it back to the proper type
		// Try to convert from map to the appropriate response type
		if mapData, ok := cached.(map[string]interface{}); ok {
			// Marshal back to JSON then unmarshal to proper type
			jsonData, err := json.Marshal(mapData)
			if err != nil {
				log.Warn().Err(err).Msg("failed to marshal cached data")
				return fetchFunc()
			}
			
			// Determine the type based on cache key and unmarshal
			switch cacheKey {
			case "models:list":
				var resp ModelsResponse
				if err := json.Unmarshal(jsonData, &resp); err != nil {
					log.Warn().Err(err).Msg("failed to unmarshal models response")
					return fetchFunc()
				}
				resp.CacheStatus = CacheStatusHit
				return &resp, nil
			case "providers:list":
				var resp ProvidersResponse
				if err := json.Unmarshal(jsonData, &resp); err != nil {
					log.Warn().Err(err).Msg("failed to unmarshal providers response")
					return fetchFunc()
				}
				resp.CacheStatus = CacheStatusHit
				return &resp, nil
			}
		}
		
		// If it's already the correct type (shouldn't happen with current cache implementation)
		switch v := cached.(type) {
		case *ModelsResponse:
			v.CacheStatus = CacheStatusHit
			return v, nil
		case *ProvidersResponse:
			v.CacheStatus = CacheStatusHit
			return v, nil
		}
		
		// If we can't handle the cached data, fetch fresh
		log.Warn().Msgf("unexpected cache type for %s: %T", cacheKey, cached)
		return fetchFunc()
	}

	// Fetch from service
	resp, err := fetchFunc()
	if err != nil {
		return nil, err
	}

	// Set cache status on the response
	switch v := resp.(type) {
	case *ModelsResponse:
		v.CacheStatus = CacheStatusMiss
	case *ProvidersResponse:
		v.CacheStatus = CacheStatusMiss
	}

	// Cache asynchronously
	go func() {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		if err := s.cacheManager.SetModel(cacheCtx, cacheKey, resp); err != nil {
			log.Warn().Err(err).Msgf("failed to cache %s", cacheMsg)
		}
	}()

	return resp, nil
}

// ListModels with caching
func (s *cachedService) ListModels(ctx context.Context) (*ModelsResponse, error) {
	if !s.cacheConfig.EnableModelCache {
		return s.service.ListModels(ctx)
	}

	resp, err := s.cacheListResponse(ctx, "models:list", "models", 
		func() (interface{}, error) { return s.service.ListModels(ctx) })
	if err != nil {
		return nil, err
	}
	return resp.(*ModelsResponse), nil
}

// ListProviders with caching
func (s *cachedService) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	if !s.cacheConfig.EnableProviderCache {
		return s.service.ListProviders(ctx)
	}

	resp, err := s.cacheListResponse(ctx, "providers:list", "providers",
		func() (interface{}, error) { return s.service.ListProviders(ctx) })
	if err != nil {
		return nil, err
	}
	return resp.(*ProvidersResponse), nil
}

// GetModelEndpoints with caching
func (s *cachedService) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	if !s.cacheConfig.EnableModelCache {
		return s.service.GetModelEndpoints(ctx, modelID)
	}

	cacheKey := fmt.Sprintf("model:endpoints:%s", modelID)

	// Try to get from cache using GetModel
	cached, found, err := s.cacheManager.GetModel(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Msg("model endpoints cache get error")
		return s.service.GetModelEndpoints(ctx, modelID)
	}

	if found {
		// Type assert to ModelEndpointsResponse
		if cachedResp, ok := cached.(*ModelEndpointsResponse); ok {
			return cachedResp, nil
		}
	}

	resp, err := s.service.GetModelEndpoints(ctx, modelID)
	if err != nil {
		return nil, err
	}

	go func() {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		if err := s.cacheManager.SetModel(cacheCtx, cacheKey, resp); err != nil {
			log.Warn().Err(err).Msg("failed to cache model endpoints")
		}
	}()

	return resp, nil
}

// shouldSkipCache checks if caching should be skipped
func (s *cachedService) shouldSkipCache(ctx context.Context, model string) bool {
	// Check for cache control header
	if ctx.Value(contextKey(s.cacheConfig.CacheControlHeader)) == "no-cache" {
		return true
	}

	// Check skip models
	for _, skipModel := range s.cacheConfig.SkipCacheModels {
		if strings.Contains(model, skipModel) {
			return true
		}
	}

	return false
}

// generateChatCacheKey generates a cache key for chat completions
func (s *cachedService) generateChatCacheKey(req *ChatCompletionRequest) (string, error) {
	// Create a normalized version of the request for consistent hashing
	normalized := struct {
		Model            string
		Messages         []connectors.Message
		Temperature      *float32
		TopP             *float32
		MaxTokens        *int
		PresencePenalty  *float32
		FrequencyPenalty *float32
		Stop             []string
		Seed             *int
		Tools            []connectors.Tool
		ToolChoice       interface{}
		ResponseFormat   *connectors.ResponseFormat
	}{
		Model:            req.Model,
		Messages:         req.Messages,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		MaxTokens:        req.MaxTokens,
		PresencePenalty:  req.PresencePenalty,
		FrequencyPenalty: req.FrequencyPenalty,
		Stop:             req.Stop,
		Seed:             req.Seed,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
	}

	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	hash := sha256.Sum256(data)
	return fmt.Sprintf("chat:%s", hex.EncodeToString(hash[:16])), nil
}

// generateEmbeddingsCacheKey generates a cache key for embeddings
func (s *cachedService) generateEmbeddingsCacheKey(req *EmbeddingsRequest) (string, error) {
	normalized := struct {
		Model          string
		Input          interface{}
		EncodingFormat string
		Dimensions     *int
	}{
		Model:          req.Model,
		Input:          req.Input,
		EncodingFormat: req.EncodingFormat,
		Dimensions:     req.Dimensions,
	}

	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	hash := sha256.Sum256(data)
	return fmt.Sprintf("embedding:%s", hex.EncodeToString(hash[:16])), nil
}

// cachedStream wraps a stream to track metrics but doesn't cache
type cachedStream struct {
	stream ChatCompletionStreamResponse
}

func (s *cachedStream) Read() (*connectors.ChatStreamChunk, error) {
	return s.stream.Read()
}

func (s *cachedStream) Close() error {
	return s.stream.Close()
}

var _ io.Closer = (*cachedStream)(nil)

// cachedStreamResponse serves a cached response as a stream
type cachedStreamResponse struct {
	response          *cache.ChatCompletionResponse
	modelID           string
	cacheStatus       string
	cacheAge          int
	reasoningPosition int
	contentPosition   int
	chunkSize         int
	sentRole          bool
	sentUsage         bool
	content           string
	reasoning         string
}

// newCachedStreamResponse creates a stream from a cached response
func newCachedStreamResponse(response *cache.ChatCompletionResponse, modelID, cacheStatus string) *cachedStreamResponse {
	// Extract content from the response
	var content, reasoning string
	if len(response.Choices) > 0 {
		content, _ = response.Choices[0].Message.Content.(string)
		reasoning = response.Choices[0].Message.Reasoning
	}
	
	// Calculate cache age
	cacheAge := 0
	if response.CachedAt > 0 {
		cacheAge = int(time.Now().Unix() - response.CachedAt)
	}
	
	return &cachedStreamResponse{
		response:          response,
		modelID:           modelID,
		cacheStatus:       cacheStatus,
		cacheAge:          cacheAge,
		reasoningPosition: 0,
		contentPosition:   0,
		chunkSize:         20, // Emit chunks of roughly 20 characters
		content:           content,
		reasoning:         reasoning,
	}
}

// Read implements ChatCompletionStreamResponse
func (r *cachedStreamResponse) Read() (*connectors.ChatStreamChunk, error) {
	// If we've sent everything, return EOF
	if r.sentUsage {
		return nil, io.EOF
	}
	
	// If we're done with both reasoning and content, send usage data
	if r.sentRole && r.reasoningPosition >= len(r.reasoning) && r.contentPosition >= len(r.content) && !r.sentUsage {
		r.sentUsage = true
		// Send final chunk with usage data
		return &connectors.ChatStreamChunk{
			ID:      r.response.ID,
			Object:  "chat.completion.chunk",
			Created: r.response.Created,
			Model:   r.modelID,
			Choices: []connectors.StreamChoice{
				{
					Index:        0,
					Delta:        connectors.MessageDelta{},
					FinishReason: "stop",
				},
			},
			Usage: r.response.Usage,
		}, nil
	}

	// First chunk - send role
	if !r.sentRole {
		r.sentRole = true
		return &connectors.ChatStreamChunk{
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
		}, nil
	}

	// Stream reasoning chunks if present
	if r.reasoning != "" && r.reasoningPosition < len(r.reasoning) {
		endPos := r.reasoningPosition + r.chunkSize
		if endPos > len(r.reasoning) {
			endPos = len(r.reasoning)
		}

		chunk := r.reasoning[r.reasoningPosition:endPos]
		r.reasoningPosition = endPos

		return &connectors.ChatStreamChunk{
			ID:      r.response.ID,
			Object:  "chat.completion.chunk",
			Created: r.response.Created,
			Model:   r.modelID,
			Choices: []connectors.StreamChoice{
				{
					Index: 0,
					Delta: connectors.MessageDelta{
						Reasoning: chunk,
					},
				},
			},
		}, nil
	}

	// Stream content chunks
	if r.contentPosition < len(r.content) {
		endPos := r.contentPosition + r.chunkSize
		if endPos > len(r.content) {
			endPos = len(r.content)
		}

		chunk := r.content[r.contentPosition:endPos]
		r.contentPosition = endPos

		return &connectors.ChatStreamChunk{
			ID:      r.response.ID,
			Object:  "chat.completion.chunk",
			Created: r.response.Created,
			Model:   r.modelID,
			Choices: []connectors.StreamChoice{
				{
					Index: 0,
					Delta: connectors.MessageDelta{
						Content: chunk,
					},
				},
			},
		}, nil
	}

	// This should not happen
	return nil, io.EOF
}

// Close implements io.Closer
func (r *cachedStreamResponse) Close() error {
	return nil
}

// GetCacheStatus implements CacheStatusProvider
func (r *cachedStreamResponse) GetCacheStatus() string {
	return r.cacheStatus
}

// GetCacheAge implements CacheStatusProvider
func (r *cachedStreamResponse) GetCacheAge() int {
	return r.cacheAge
}

// Ensure cachedStreamResponse implements our interfaces
var _ ChatCompletionStreamResponse = (*cachedStreamResponse)(nil)
var _ CacheStatusProvider = (*cachedStreamResponse)(nil)

// cachingStreamWrapper wraps a stream to cache the complete response
type cachingStreamWrapper struct {
	ctx          context.Context
	stream       ChatCompletionStreamResponse
	cacheManager CacheManager
	cacheKey     string
	modelID      string
	chunks       []connectors.ChatStreamChunk
	cacheStatus  string
}

// newCachingStreamWrapper creates a wrapper that caches the streamed response
func newCachingStreamWrapper(ctx context.Context, stream ChatCompletionStreamResponse, cm CacheManager, cacheKey, modelID string) *cachingStreamWrapper {
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

// Read implements ChatCompletionStreamResponse
func (w *cachingStreamWrapper) Read() (*connectors.ChatStreamChunk, error) {
	chunk, err := w.stream.Read()
	
	// Store chunk even if it comes with EOF (final chunk often has usage data)
	if chunk != nil {
		w.chunks = append(w.chunks, *chunk)
	}
	
	if err != nil {
		if err == io.EOF && len(w.chunks) > 0 {
			// Stream completed, cache the accumulated response
			go w.cacheResponse()
		}
		return chunk, err
	}

	return chunk, nil
}

// Close implements io.Closer
func (w *cachingStreamWrapper) Close() error {
	return w.stream.Close()
}

// GetCacheStatus implements CacheStatusProvider
func (w *cachingStreamWrapper) GetCacheStatus() string {
	return w.cacheStatus
}

// GetCacheAge implements CacheStatusProvider
func (w *cachingStreamWrapper) GetCacheAge() int {
	// For cache misses, return 0
	return 0
}

// cacheResponse reconstructs and caches the complete response
func (w *cachingStreamWrapper) cacheResponse() {
	// Reconstruct the complete response from chunks
	response := w.reconstructResponse()
	if response == nil {
		return
	}

	// Convert to cache response
	cacheResp := &cache.ChatCompletionResponse{
		ID:                response.ID,
		Object:            response.Object,
		Created:           response.Created,
		Model:             response.Model,
		Choices:           response.Choices,
		Usage:             response.Usage,
		SystemFingerprint: response.SystemFingerprint,
		ModelUsed:         response.ModelUsed,
		CachedAt:          time.Now().Unix(),
	}
	
	// Use a new context with timeout for caching
	cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	// Cache the response
	if err := w.cacheManager.SetChatCompletion(cacheCtx, w.cacheKey, cacheResp); err != nil {
		log.Warn().
			Err(err).
			Str("model", w.modelID).
			Msg("failed to cache streaming response")
	} else {
		log.Info().
			Str("model", w.modelID).
			Str("cache_key", w.cacheKey).
			Msg("cached streaming response")
	}
}

// reconstructResponse builds a complete response from accumulated chunks
func (w *cachingStreamWrapper) reconstructResponse() *ChatCompletionResponse {
	if len(w.chunks) == 0 {
		return nil
	}

	// Get basic info from first chunk
	firstChunk := w.chunks[0]
	
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
	var role string
	for _, chunk := range w.chunks {
		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta.Role != "" {
				role = choice.Delta.Role
			}
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

	// Get the actual model used from the chunks (may differ from requested model)
	modelUsed := ""
	if len(w.chunks) > 0 && w.chunks[0].Model != "" {
		modelUsed = w.chunks[0].Model
	}
	
	// Build the complete response
	return &ChatCompletionResponse{
		ID:        firstChunk.ID,
		Object:    "chat.completion",
		Created:   firstChunk.Created,
		Model:     w.modelID,
		ModelUsed: modelUsed,
		Choices: []connectors.Choice{
			{
				Index: 0,
				Message: connectors.Message{
					Role:      role,
					Content:   content,
					Reasoning: reasoning,
				},
				FinishReason: finishReason,
			},
		},
		Usage:       usage,
		CacheStatus: CacheStatusMiss,
	}
}

// Ensure cachingStreamWrapper implements our interfaces
var _ ChatCompletionStreamResponse = (*cachingStreamWrapper)(nil)
var _ CacheStatusProvider = (*cachingStreamWrapper)(nil)