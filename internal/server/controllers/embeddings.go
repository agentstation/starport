package controllers

import (
	"fmt"
	"net/http"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/proxy"
)

// EmbeddingsController handles embeddings endpoints
type EmbeddingsController struct {
	*BaseHandler
}

// NewEmbeddingsController creates a new embeddings controller
func NewEmbeddingsController(service proxy.Proxy) *EmbeddingsController {
	return newEmbeddingsController(service, ProtocolOpenAI)
}

// NewOpenRouterEmbeddingsController creates an OpenRouter embeddings controller.
func NewOpenRouterEmbeddingsController(service proxy.Proxy) *EmbeddingsController {
	return newEmbeddingsController(service, ProtocolOpenRouter)
}

func newEmbeddingsController(service proxy.Proxy, protocol Protocol) *EmbeddingsController {
	return &EmbeddingsController{
		BaseHandler: NewProtocolBaseHandler(service, protocol),
	}
}

// Create handles POST /v1/embeddings and /api/v1/embeddings
func (h *EmbeddingsController) Create(w http.ResponseWriter, r *http.Request) {
	request, err := h.decodeEmbedding(r)
	if err != nil {
		h.writeBodyRefusal(w, err)
		return
	}
	req := &proxy.EmbeddingsRequest{Request: request}

	// Check If-None-Match header for conditional request
	ifNoneMatch := r.Header.Get("If-None-Match")

	// Add context from HTTP request
	ctx := r.Context()
	req.APIKey = h.getAPIKey(ctx)
	req.TenantID = h.getTenantID(ctx)
	req.KeyID = h.getAPIKeyID(ctx)
	req.APIKeyConfig, err = h.getAPIKeyRoutingConfig(ctx)
	if err != nil {
		h.writeCredentialStrategyError(w, err)
		return
	}
	req.RequestID = h.getRequestID(ctx)
	req.Protocol = string(h.protocol)

	// Process the request
	resp, err := h.service.ProcessEmbeddings(ctx, req)
	if err != nil {
		h.logError(ctx, err, "embeddings generation failed")
		h.writeError(w, err)
		return
	}

	// Set cache headers from response
	if resp.CacheStatus != "" {
		w.Header().Set("X-Cache", resp.CacheStatus)
		if resp.CacheStatus == "HIT" && resp.CacheAge > 0 {
			w.Header().Set("X-Cache-Age", fmt.Sprintf("%d", resp.CacheAge))
		}
	}

	// Set ETag header
	if resp.ETag != "" {
		w.Header().Set("ETag", resp.ETag)

		// Check for 304 Not Modified
		if ifNoneMatch != "" && ifNoneMatch == resp.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	if err := h.writeEmbeddingResponse(w, resp.Response); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}

func (h *EmbeddingsController) decodeEmbedding(r *http.Request) (inference.EmbeddingRequest, error) {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.DecodeEmbedding(r.Body)
	}
	return openai.DecodeEmbedding(r.Body)
}

func (h *EmbeddingsController) writeEmbeddingResponse(w http.ResponseWriter, response inference.EmbeddingResponse) error {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.WriteJSON(w, http.StatusOK, openrouter.EncodeEmbedding(response))
	}
	return openai.WriteJSON(w, http.StatusOK, openai.EncodeEmbedding(response))
}
