package handlers

import (
	"net/http"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// EmbeddingsHandler handles embeddings endpoints
type EmbeddingsHandler struct {
	*BaseHandler
}

// NewEmbeddingsHandler creates a new embeddings handler
func NewEmbeddingsHandler(service proxy.Service) *EmbeddingsHandler {
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
	}

	// Write response
	if err := dto.WriteJSON(w, http.StatusOK, resp); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}
