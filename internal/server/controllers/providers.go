package controllers

import (
	"net/http"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// ProvidersController handles provider-related endpoints
type ProvidersController struct {
	*BaseHandler
}

// NewProvidersController creates a new providers controller
func NewProvidersController(service proxy.Proxy) *ProvidersController {
	return &ProvidersController{
		BaseHandler: NewBaseHandler(service),
	}
}

// List handles GET /api/v1/providers
func (h *ProvidersController) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get providers from service
	resp, err := h.service.ListProviders(ctx)
	if err != nil {
		h.logError(ctx, err, "failed to list providers")
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
