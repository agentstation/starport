package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	responsecache "github.com/agentstation/starport/internal/response/cache"
	"github.com/rs/zerolog/log"
)

// CacheManager interface defines the cache operations used by the proxy
type CacheManager interface {
	GetModel(ctx context.Context, key string) (any, bool, error)
	SetModel(ctx context.Context, key string, value any) error
	GetResponse(ctx context.Context, key string) ([]byte, bool, error)
	SetResponse(ctx context.Context, key string, response []byte) error
}

type runtimeGenerationSource interface {
	AcquireRuntime() (connectors.RuntimeLease, error)
}

// CacheMiddleware provides caching functionality for proxy services.
type CacheMiddleware struct {
	manager CacheManager
	config  *CacheConfig
	runtime runtimeGenerationSource
}

// NewCacheMiddleware creates a new cache middleware.
func NewCacheMiddleware(manager CacheManager, config *CacheConfig, runtime runtimeGenerationSource) Middleware {
	return &CacheMiddleware{
		manager: manager,
		config:  config,
		runtime: runtime,
	}
}

// Wrap wraps the service with caching functionality.
func (m *CacheMiddleware) Wrap(next Proxy) Proxy {
	return &cachedService{
		service:      next,
		cacheManager: m.manager,
		cacheConfig:  *m.config,
		runtime:      m.runtime,
	}
}

// cachedService implements the Proxy interface with caching
type cachedService struct {
	service      Proxy
	cacheManager CacheManager
	cacheConfig  CacheConfig
	runtime      runtimeGenerationSource
	generation   string
}

// ProcessChatCompletion handles chat completions with Manager-based caching
func (s *cachedService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	log.Info().
		Str("model", req.Request.Model).
		Bool("stream", req.Request.Stream).
		Bool("cache_enabled", s.cacheConfig.EnableChatCache).
		Msg("CachedService.ProcessChatCompletion called")

	// Skip cache for streaming requests or if caching is disabled
	if req.Request.Stream || !s.cacheConfig.EnableChatCache || s.shouldSkipCache(ctx, req.Request.Model) {
		return s.service.ProcessChatCompletion(ctx, req)
	}
	ctx, runtime, owned := s.runtimeContext(ctx)
	if owned {
		defer runtime.Release()
	}
	if s.runtime != nil && runtime == nil {
		return s.service.ProcessChatCompletion(ctx, req)
	}

	// Generate cache key
	cacheKey, err := s.generateChatCacheKey(ctx, req)
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate cache key")
		return s.service.ProcessChatCompletion(ctx, req)
	}

	repository, err := responsecache.Open(s.cacheManager, nil)
	if err != nil {
		return s.service.ProcessChatCompletion(ctx, req)
	}
	cachedResp, cachedAt, found, err := repository.GetChat(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Str("key", cacheKey).Msg("cache get error")
		// Continue without cache on error
		return s.service.ProcessChatCompletion(ctx, req)
	}

	if found {
		log.Info().
			Str("model", req.Request.Model).
			Str("cache_key", cacheKey).
			Msg("cache hit for chat completion")

		resp, err := chatResponseFromCanonical(cachedResp)
		if err != nil {
			return s.service.ProcessChatCompletion(ctx, req)
		}
		resp.CacheStatus = CacheStatusHit
		resp.CacheAge = cacheAge(cachedAt)
		return resp, nil
	}

	log.Info().
		Str("model", req.Request.Model).
		Str("cache_key", cacheKey).
		Msg("cache miss for chat completion")

	// Execute the request
	resp, err := s.service.ProcessChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	// Mark as cache miss
	resp.CacheStatus = CacheStatusMiss

	// Cache the response after the upstream request completes. This is
	// intentionally synchronous so cache writes remain owned by the request
	// path and race tests can reason about completion deterministically.
	cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	canonical, conversionErr := chatResponseToCanonical(resp)
	if conversionErr == nil {
		conversionErr = repository.PutChat(cacheCtx, cacheKey, canonical)
	}
	if conversionErr != nil {
		log.Warn().Err(conversionErr).Str("key", cacheKey).Msg("failed to cache response")
	}

	return resp, nil
}

// ProcessChatCompletionStream handles streaming requests with caching
func (s *cachedService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	log.Info().
		Str("model", req.Request.Model).
		Bool("cache_enabled", s.cacheConfig.EnableChatCache).
		Msg("CachedService.ProcessChatCompletionStream called")

	// Skip cache if disabled or should skip
	if !s.cacheConfig.EnableChatCache || s.shouldSkipCache(ctx, req.Request.Model) {
		return s.service.ProcessChatCompletionStream(ctx, req)
	}
	ctx, runtime, owned := s.runtimeContext(ctx)
	finish := func(
		stream ChatCompletionStreamResponse,
		err error,
	) (ChatCompletionStreamResponse, error) {
		if err != nil {
			if owned {
				runtime.Release()
			}
			return nil, err
		}
		if stream == nil {
			if owned {
				runtime.Release()
			}
			return nil, errors.New("stream response is required")
		}
		if owned {
			return newRuntimeLeaseStream(stream, runtime), nil
		}
		return stream, nil
	}
	if s.runtime != nil && runtime == nil {
		return s.service.ProcessChatCompletionStream(ctx, req)
	}

	// Generate cache key - same as non-streaming
	cacheKey, err := s.generateChatCacheKey(ctx, req)
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate cache key for streaming")
		return finish(s.service.ProcessChatCompletionStream(ctx, req))
	}

	repository, err := responsecache.Open(s.cacheManager, nil)
	if err != nil {
		return finish(s.service.ProcessChatCompletionStream(ctx, req))
	}
	canonicalRequest, err := canonicalChatRequest(req)
	if err != nil {
		return finish(s.service.ProcessChatCompletionStream(ctx, req))
	}
	cachedResp, cachedAt, found, err := repository.GetChat(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Str("key", cacheKey).Msg("cache get error for streaming")
		// Continue without cache on error
		stream, err := s.service.ProcessChatCompletionStream(ctx, req)
		if err != nil {
			return finish(nil, err)
		}
		return finish(newCachingStreamWrapper(stream, repository, cacheKey), nil)
	}

	if found {
		log.Info().
			Str("model", req.Request.Model).
			Str("cache_key", cacheKey).
			Msg("cache hit for streaming chat completion")

		events, err := responsecache.StreamEvents(cachedResp, canonicalRequest.StreamOptions)
		if err != nil {
			return finish(s.service.ProcessChatCompletionStream(ctx, req))
		}
		return finish(newCachedEventStream(events, cachedAt), nil)
	}

	log.Info().
		Str("model", req.Request.Model).
		Str("cache_key", cacheKey).
		Msg("cache miss for streaming chat completion")

	// Cache miss - get stream from service
	stream, err := s.service.ProcessChatCompletionStream(ctx, req)
	if err != nil {
		return finish(nil, err)
	}

	// Wrap stream to cache the response
	return finish(newCachingStreamWrapper(stream, repository, cacheKey), nil)
}

// ProcessEmbeddings handles embeddings with caching
func (s *cachedService) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Skip cache if disabled
	if !s.cacheConfig.EnableEmbeddingCache || s.shouldSkipCache(ctx, req.Request.Model) {
		return s.service.ProcessEmbeddings(ctx, req)
	}
	ctx, runtime, owned := s.runtimeContext(ctx)
	if owned {
		defer runtime.Release()
	}
	if s.runtime != nil && runtime == nil {
		return s.service.ProcessEmbeddings(ctx, req)
	}

	// Generate cache key
	cacheKey, err := s.generateEmbeddingsCacheKey(ctx, req)
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate embeddings cache key")
		return s.service.ProcessEmbeddings(ctx, req)
	}

	repository, err := responsecache.Open(s.cacheManager, nil)
	if err != nil {
		return s.service.ProcessEmbeddings(ctx, req)
	}
	cachedResp, cachedAt, found, err := repository.GetEmbedding(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Str("key", cacheKey).Msg("embeddings cache get error")
		return s.service.ProcessEmbeddings(ctx, req)
	}

	if found {
		log.Debug().
			Str("model", req.Request.Model).
			Msg("cache hit for embedding")

		resp := embeddingResponseFromCanonical(cachedResp)
		resp.CacheStatus = CacheStatusHit
		resp.CacheAge = cacheAge(cachedAt)
		return resp, nil
	}

	// Execute the request
	resp, err := s.service.ProcessEmbeddings(ctx, req)
	if err != nil {
		return nil, err
	}

	resp.CacheStatus = CacheStatusMiss

	cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := repository.PutEmbedding(cacheCtx, cacheKey, embeddingResponseToCanonical(resp)); err != nil {
		log.Warn().Err(err).Str("key", cacheKey).Msg("failed to cache embeddings")
	}

	return resp, nil
}

// cacheListResponse is a helper for caching list responses (models, providers)
// The three media operations pass straight through. A generated image or
// audio file is large and a caller that repeats a prompt expects a new
// rendering, so there is nothing here to cache.

// ProcessImages routes one image request without caching it.
func (s *cachedService) ProcessImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error) {
	return s.service.ProcessImages(ctx, req)
}

// ProcessSpeech routes one speech request without caching it.
func (s *cachedService) ProcessSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	return s.service.ProcessSpeech(ctx, req)
}

// ProcessTranscription routes one transcription request without caching it.
func (s *cachedService) ProcessTranscription(
	ctx context.Context,
	req *TranscriptionRequest,
) (*TranscriptionResponse, error) {
	return s.service.ProcessTranscription(ctx, req)
}

func (s *cachedService) cacheListResponse(ctx context.Context, cacheKey, cacheMsg string, fetchFunc func() (any, error)) (any, error) {
	// Try to get from cache
	cached, found, err := s.cacheManager.GetModel(ctx, cacheKey)
	if err != nil {
		log.Warn().Err(err).Msgf("%s cache get error", cacheMsg)
		return fetchFunc()
	}

	if found {
		// The cache returns a generic map, we need to convert it back to the proper type
		// Try to convert from map to the appropriate response type
		if mapData, ok := cached.(map[string]any); ok {
			// Marshal back to JSON then unmarshal to proper type
			jsonData, err := json.Marshal(mapData)
			if err != nil {
				log.Warn().Err(err).Msg("failed to marshal cached data")
				return fetchFunc()
			}

			// Determine the type based on cache key and unmarshal
			switch {
			case strings.HasPrefix(cacheKey, "models:list:"):
				var resp ModelsResponse
				if err := json.Unmarshal(jsonData, &resp); err != nil {
					log.Warn().Err(err).Msg("failed to unmarshal models response")
					return fetchFunc()
				}
				resp.CacheStatus = CacheStatusHit
				return &resp, nil
			case strings.HasPrefix(cacheKey, "providers:list:"):
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

	cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.cacheManager.SetModel(cacheCtx, cacheKey, resp); err != nil {
		log.Warn().Err(err).Msgf("failed to cache %s", cacheMsg)
	}

	return resp, nil
}

// ListModels with caching
func (s *cachedService) ListModels(ctx context.Context) (*ModelsResponse, error) {
	if !s.cacheConfig.EnableModelCache {
		return s.service.ListModels(ctx)
	}
	ctx, runtime, owned := s.runtimeContext(ctx)
	if owned {
		defer runtime.Release()
	}
	if s.runtime != nil && runtime == nil {
		return s.service.ListModels(ctx)
	}
	cacheKey := "models:list:" + s.catalogGeneration(ctx)

	resp, err := s.cacheListResponse(ctx, cacheKey, "models",
		func() (any, error) { return s.service.ListModels(ctx) })
	if err != nil {
		return nil, err
	}
	return resp.(*ModelsResponse), nil
}

// ListAuthors delegates to the wrapped service. Author projections read
// one immutable catalog snapshot, so a response cache adds no value.
func (s *cachedService) ListAuthors(ctx context.Context) (*AuthorsResponse, error) {
	return s.service.ListAuthors(ctx)
}

// GetAuthor delegates to the wrapped service.
func (s *cachedService) GetAuthor(ctx context.Context, authorID string) (*AuthorInfo, error) {
	return s.service.GetAuthor(ctx, authorID)
}

// GetLogo delegates to the wrapped service. Logo bytes come from the
// in-memory catalog snapshot, so a cache layer adds no value.
func (s *cachedService) GetLogo(ctx context.Context, kind view.LogoKind, id string) ([]byte, error) {
	return s.service.GetLogo(ctx, kind, id)
}

// ListProviders with caching
func (s *cachedService) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	if !s.cacheConfig.EnableProviderCache {
		return s.service.ListProviders(ctx)
	}
	ctx, runtime, owned := s.runtimeContext(ctx)
	if owned {
		defer runtime.Release()
	}
	if s.runtime != nil && runtime == nil {
		return s.service.ListProviders(ctx)
	}
	cacheKey := "providers:list:" + s.catalogGeneration(ctx)

	resp, err := s.cacheListResponse(ctx, cacheKey, "providers",
		func() (any, error) { return s.service.ListProviders(ctx) })
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
	ctx, runtime, owned := s.runtimeContext(ctx)
	if owned {
		defer runtime.Release()
	}
	if s.runtime != nil && runtime == nil {
		return s.service.GetModelEndpoints(ctx, modelID)
	}

	cacheKey := fmt.Sprintf("model:endpoints:%s:%s", s.catalogGeneration(ctx), modelID)

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

	cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.cacheManager.SetModel(cacheCtx, cacheKey, resp); err != nil {
		log.Warn().Err(err).Msg("failed to cache model endpoints")
	}

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
func (s *cachedService) generateChatCacheKey(
	ctx context.Context,
	req *ChatCompletionRequest,
) (string, error) {
	canonical, err := canonicalChatRequest(req)
	if err != nil {
		return "", err
	}
	return responsecache.ChatKey(responsecache.ChatIdentity{
		TenantID:          req.TenantID,
		CatalogGeneration: s.catalogGeneration(ctx),
		Request:           canonical,
		Policy:            cachePolicy(req.Route, req.Provider, req.APIKeyConfig),
	})
}

// generateEmbeddingsCacheKey generates a cache key for embeddings
func (s *cachedService) generateEmbeddingsCacheKey(
	ctx context.Context,
	req *EmbeddingsRequest,
) (string, error) {
	return responsecache.EmbeddingKey(responsecache.EmbeddingIdentity{
		TenantID:          req.TenantID,
		CatalogGeneration: s.catalogGeneration(ctx),
		Request:           req.Request,
		Policy:            cachePolicy("", nil, req.APIKeyConfig),
	})
}

func (s *cachedService) catalogGeneration(ctx context.Context) string {
	if runtime := connectors.RuntimeLeaseFromContext(ctx); runtime != nil {
		if snapshot := runtime.Snapshot(); snapshot != nil {
			return snapshot.GenerationID()
		}
	}
	return s.generation
}

func (s *cachedService) runtimeContext(
	ctx context.Context,
) (context.Context, connectors.RuntimeLease, bool) {
	if runtime := connectors.RuntimeLeaseFromContext(ctx); runtime != nil {
		return ctx, runtime, false
	}
	if s.runtime == nil {
		return ctx, nil, false
	}
	runtime, err := s.runtime.AcquireRuntime()
	if err != nil {
		log.Warn().Err(err).Msg("failed to retain provider runtime for cache operation")
		return ctx, nil, false
	}
	return connectors.ContextWithRuntimeLease(ctx, runtime), runtime, true
}

func canonicalChatRequest(req *ChatCompletionRequest) (inference.ChatRequest, error) {
	if req == nil {
		return inference.ChatRequest{}, errors.New("canonical chat request is required")
	}
	return req.Request.Clone(), nil
}

func cachePolicy(route string, provider *ProviderPreferences, tenant *APIKeyRoutingConfig) responsecache.Policy {
	policy := responsecache.Policy{}
	policy.Provider.Route = route
	if provider != nil {
		policy.Provider.Order = append([]string(nil), provider.Order...)
		policy.Provider.Only = append([]string(nil), provider.Only...)
		policy.Provider.Ignore = append([]string(nil), provider.Ignore...)
		policy.Provider.AllowFallbacks = provider.AllowFallback
		policy.Provider.Sort = provider.Sort
		policy.Provider.MaxPromptPricePer1M = provider.MaxPromptPricePer1M
		policy.Provider.MaxCompletionPricePer1M = provider.MaxCompletionPricePer1M
	}
	if tenant != nil {
		policy.Tenant.AllowedModels = append([]string(nil), tenant.AllowedModels...)
		policy.Tenant.AllowedProviders = append([]string(nil), tenant.AllowedProviders...)
		policy.Tenant.RateLimitTier = tenant.RateLimitTier
		policy.Tenant.CredentialStrategy = string(tenant.CredentialStrategy)
		policy.Provider.ModelOverrides = make(map[string]string, len(tenant.ModelOverrides))
		for model, override := range tenant.ModelOverrides {
			policy.Provider.ModelOverrides[model] = override
		}
		if len(policy.Provider.ModelOverrides) == 0 {
			policy.Provider.ModelOverrides = nil
		}
	}
	return policy
}

func chatResponseToCanonical(response *ChatCompletionResponse) (inference.ChatResponse, error) {
	if response == nil {
		return inference.ChatResponse{}, errors.New("canonical chat response is required")
	}
	return response.Response.Clone(), nil
}

func chatResponseFromCanonical(response inference.ChatResponse) (*ChatCompletionResponse, error) {
	return &ChatCompletionResponse{Response: response.Clone()}, nil
}

func embeddingResponseToCanonical(response *EmbeddingsResponse) inference.EmbeddingResponse {
	if response == nil {
		return inference.EmbeddingResponse{}
	}
	return response.Response.Clone()
}

func embeddingResponseFromCanonical(response inference.EmbeddingResponse) *EmbeddingsResponse {
	return &EmbeddingsResponse{Response: response.Clone()}
}

func cacheAge(cachedAt time.Time) int {
	if cachedAt.IsZero() {
		return 0
	}
	age := time.Since(cachedAt)
	if age < 0 {
		return 0
	}
	return int(age / time.Second)
}

// cachedEventStream replays canonical events from one completed result.
type cachedEventStream struct {
	events   []inference.StreamEvent
	position int
	cachedAt time.Time
}

func newCachedEventStream(events []inference.StreamEvent, cachedAt time.Time) *cachedEventStream {
	clones := make([]inference.StreamEvent, len(events))
	for index, event := range events {
		clones[index] = event.Clone()
	}
	return &cachedEventStream{events: clones, cachedAt: cachedAt}
}

func (s *cachedEventStream) Read() (*inference.StreamEvent, error) {
	if s.position >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.position].Clone()
	s.position++
	return &event, nil
}

func (s *cachedEventStream) Close() error           { return nil }
func (s *cachedEventStream) GetCacheStatus() string { return CacheStatusHit }
func (s *cachedEventStream) GetCacheAge() int       { return cacheAge(s.cachedAt) }

var _ ChatCompletionStreamResponse = (*cachedEventStream)(nil)
var _ CacheStatusProvider = (*cachedEventStream)(nil)

// cachingStreamWrapper stores only a successfully completed canonical stream.
type cachingStreamWrapper struct {
	stream     ChatCompletionStreamResponse
	repository responsecache.Repository
	cacheKey   string
	events     []inference.StreamEvent
	cached     bool
}

func newCachingStreamWrapper(
	stream ChatCompletionStreamResponse,
	repository responsecache.Repository,
	cacheKey string,
) *cachingStreamWrapper {
	return &cachingStreamWrapper{
		stream: stream, repository: repository, cacheKey: cacheKey,
		events: make([]inference.StreamEvent, 0),
	}
}

func (w *cachingStreamWrapper) Read() (*inference.StreamEvent, error) {
	event, err := w.stream.Read()
	if event != nil {
		w.events = append(w.events, event.Clone())
	}
	if err == io.EOF && !w.cached && len(w.events) > 0 {
		w.cached = true
		w.cacheResponse()
	}
	return event, err
}

func (w *cachingStreamWrapper) Close() error                         { return w.stream.Close() }
func (w *cachingStreamWrapper) GetCacheStatus() string               { return CacheStatusMiss }
func (w *cachingStreamWrapper) GetCacheAge() int                     { return 0 }
func (w *cachingStreamWrapper) Unwrap() ChatCompletionStreamResponse { return w.stream }

func (w *cachingStreamWrapper) cacheResponse() {
	response, err := responsecache.CompleteStream(w.events)
	if err != nil {
		log.Warn().Err(err).Msg("failed to reconstruct canonical cached stream")
		return
	}
	cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.repository.PutChat(cacheCtx, w.cacheKey, response); err != nil {
		log.Warn().Err(err).Str("model", response.ModelUsed).Msg("failed to cache streaming response")
	}
}

var _ ChatCompletionStreamResponse = (*cachingStreamWrapper)(nil)
var _ CacheStatusProvider = (*cachingStreamWrapper)(nil)

// runtimeLeaseStream keeps the cache lookup, routed stream, and cache write on
// one complete runtime generation.
type runtimeLeaseStream struct {
	stream  ChatCompletionStreamResponse
	runtime connectors.RuntimeLease
	once    sync.Once
}

func newRuntimeLeaseStream(
	stream ChatCompletionStreamResponse,
	runtime connectors.RuntimeLease,
) ChatCompletionStreamResponse {
	if stream == nil || runtime == nil {
		return stream
	}
	return &runtimeLeaseStream{stream: stream, runtime: runtime}
}

func (s *runtimeLeaseStream) Read() (*inference.StreamEvent, error) {
	event, err := s.stream.Read()
	if err != nil {
		s.release()
	}
	return event, err
}

func (s *runtimeLeaseStream) Close() error {
	err := s.stream.Close()
	s.release()
	return err
}

func (s *runtimeLeaseStream) GetCacheStatus() string {
	if status, ok := s.stream.(CacheStatusProvider); ok {
		return status.GetCacheStatus()
	}
	return ""
}

func (s *runtimeLeaseStream) GetCacheAge() int {
	if status, ok := s.stream.(CacheStatusProvider); ok {
		return status.GetCacheAge()
	}
	return 0
}

func (s *runtimeLeaseStream) Unwrap() ChatCompletionStreamResponse { return s.stream }

func (s *runtimeLeaseStream) release() {
	s.once.Do(s.runtime.Release)
}

var _ ChatCompletionStreamResponse = (*runtimeLeaseStream)(nil)
var _ CacheStatusProvider = (*runtimeLeaseStream)(nil)
