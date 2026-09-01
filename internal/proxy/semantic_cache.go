package proxy

import (
	"context"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/inference"
	responsecache "github.com/agentstation/starport/internal/response/cache"
	"github.com/rs/zerolog/log"
)

// SemanticEmbedIdentity carries the gateway identity a cache embedding call
// runs under, so the call pays and meters like the account's own request.
type SemanticEmbedIdentity struct {
	AccountID string
	KeyID     string
	TeamID    string
	RequestID string
	Protocol  string
}

// SemanticEmbedder turns canonical prompt text into one vector.
type SemanticEmbedder interface {
	Embed(ctx context.Context, identity SemanticEmbedIdentity, text string) ([]float32, error)
}

// GatewayEmbedder adapts the gateway's own embeddings surface to the
// SemanticEmbedder seam. The embedding rides the account's own identity:
// credential selection, usage capture, and limits treat it as the
// account's own embedding request. The gateway is late-bound because
// composition finishes after the cache middleware is built.
type GatewayEmbedder struct {
	model   string
	gateway func() Proxy
}

// NewGatewayEmbedder builds the adapter around a catalog embedding model
// and a late-bound gateway.
func NewGatewayEmbedder(model string, gateway func() Proxy) *GatewayEmbedder {
	return &GatewayEmbedder{model: model, gateway: gateway}
}

// Embed implements SemanticEmbedder over the gateway's embeddings path.
func (e *GatewayEmbedder) Embed(ctx context.Context, identity SemanticEmbedIdentity, text string) ([]float32, error) {
	response, err := e.gateway().ProcessEmbeddings(ctx, &EmbeddingsRequest{
		Request: inference.EmbeddingRequest{
			Model: e.model,
			Input: inference.EmbeddingInput{Texts: []string{text}},
		},
		AccountID: identity.AccountID,
		KeyID:     identity.KeyID,
		TeamID:    identity.TeamID,
		// The embedding draws its own usage record beside the turn that
		// asked for it, so the suffix keeps the two apart while the
		// shared stem joins them.
		RequestID: identity.RequestID + "-semantic-cache",
		Protocol:  identity.Protocol,
	})
	if err != nil {
		return nil, err
	}
	if len(response.Response.Data) != 1 {
		return nil, fmt.Errorf("embedding answered %d vectors for one input", len(response.Response.Data))
	}
	return response.Response.Data[0].Vector, nil
}

// semanticProbe is one request's similarity state: the scope that pins
// everything but the prompt text, the embedded prompt vector, and the
// index they meet in. A nil probe means the layer does not apply to the
// request, and every failure on the way to one fails open: the request
// pays the provider it would have paid anyway.
type semanticProbe struct {
	index    responsecache.SemanticIndex
	scopeKey string
	vector   []float32
}

// semanticProbe builds the similarity state for one chat request, or nil
// when the layer is off, the request did not opt in, the request holds no
// prompt text, or the embedding failed.
func (s *cachedService) semanticProbe(ctx context.Context, req *ChatCompletionRequest) *semanticProbe {
	if !s.cacheConfig.EnableSemanticCache || s.cacheConfig.SemanticEmbedder == nil || !req.SemanticCache {
		return nil
	}
	canonical, err := canonicalChatRequest(req)
	if err != nil {
		return nil
	}
	scopeKey, promptText, err := responsecache.SemanticScope(responsecache.ChatIdentity{
		AccountID:         req.AccountID,
		CatalogGeneration: s.catalogGeneration(ctx),
		Request:           canonical,
		Policy:            cachePolicy(req.Route, req.Provider, req.APIKeyConfig),
	})
	if err != nil {
		log.Debug().Err(err).Msg("semantic cache does not apply to this request")
		return nil
	}
	index, err := responsecache.OpenSemanticIndex(
		s.cacheManager, s.cacheConfig.SemanticThreshold, s.cacheConfig.SemanticMaxEntries,
	)
	if err != nil {
		log.Warn().Err(err).Msg("semantic cache index unavailable")
		return nil
	}
	vector, err := s.cacheConfig.SemanticEmbedder.Embed(ctx, SemanticEmbedIdentity{
		AccountID: req.AccountID,
		KeyID:     req.KeyID,
		TeamID:    req.TeamID,
		RequestID: req.RequestID,
		Protocol:  req.Protocol,
	}, promptText)
	if err != nil || len(vector) == 0 {
		log.Warn().Err(err).Msg("semantic cache embedding failed, continuing without it")
		return nil
	}
	return &semanticProbe{index: index, scopeKey: scopeKey, vector: vector}
}

// lookup answers the cached response the nearest vector points at, when
// its similarity clears the threshold and its exact entry still exists. A
// vector whose entry has left the store is dropped with it.
func (p *semanticProbe) lookup(
	ctx context.Context,
	repository responsecache.Repository,
) (inference.ChatResponse, time.Time, float64, bool) {
	match, found, err := p.index.Lookup(ctx, p.scopeKey, p.vector)
	if err != nil {
		log.Warn().Err(err).Msg("semantic cache lookup error")
		return inference.ChatResponse{}, time.Time{}, 0, false
	}
	if !found {
		return inference.ChatResponse{}, time.Time{}, 0, false
	}
	cached, cachedAt, ok, err := repository.GetChat(ctx, match.Key)
	if err != nil || !ok {
		// The exact entry left the store, so its vector goes with it.
		if dropErr := p.index.Drop(ctx, p.scopeKey, match.Key); dropErr != nil {
			log.Warn().Err(dropErr).Msg("semantic cache failed to drop a stale vector")
		}
		return inference.ChatResponse{}, time.Time{}, 0, false
	}
	return cached, cachedAt, match.Similarity, true
}

// store records the prompt vector beside the exact entry it points at.
func (p *semanticProbe) store(ctx context.Context, exactKey string) {
	if err := p.index.Add(ctx, p.scopeKey, p.vector, exactKey); err != nil {
		log.Warn().Err(err).Msg("semantic cache failed to store a vector")
	}
}
