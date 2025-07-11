package handlers

import (
	"net/http"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// ProvidersHandler handles provider-related endpoints
type ProvidersHandler struct {
	*BaseHandler
}

// NewProvidersHandler creates a new providers handler
func NewProvidersHandler(service proxy.Service) *ProvidersHandler {
	return &ProvidersHandler{
		BaseHandler: NewBaseHandler(service),
	}
}

// List handles GET /api/v1/providers
func (h *ProvidersHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get providers from service
	resp, err := h.service.ListProviders(ctx)
	if err != nil {
		h.logError(ctx, err, "failed to list providers")
		h.writeError(w, err)
		return
	}

	// Set cache headers
	h.setCacheHeaders(ctx, w)

	// Write response
	if err := dto.WriteJSON(w, http.StatusOK, resp); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}
