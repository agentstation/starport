package handlers

import (
	"fmt"
	"net/http"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// EmbeddingsHandler handles embeddings endpoints
type EmbeddingsHandler struct {
	*BaseHandler
}

// NewEmbeddingsHandler creates a new embeddings handler
func NewEmbeddingsHandler(service proxy.Proxy) *EmbeddingsHandler {
	return &EmbeddingsHandler{
		BaseHandler: NewBaseHandler(service),
	}
}

// Create handles POST /v1/embeddings and /api/v1/embeddings
func (h *EmbeddingsHandler) Create(w http.ResponseWriter, r *http.Request) {
	// Parse request
	req, err := dto.ParseEmbeddingsRequest(r)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	// Check If-None-Match header for conditional request
	ifNoneMatch := r.Header.Get("If-None-Match")

	// Add context from HTTP request
	ctx := r.Context()
	req.APIKey = h.getAPIKey(ctx)
	req.RequestID = h.getRequestID(ctx)

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

	// Write response
	if err := dto.WriteJSON(w, http.StatusOK, resp); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}
