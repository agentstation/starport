package handlers

import (
	"net/http"
	"time"

	"github.com/agentstation/starport/internal/server/dto"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	version string
	service string
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(service, version string) *HealthHandler {
	return &HealthHandler{
		service: service,
		version: version,
	}
}

// Live handles GET /health/live
func (h *HealthHandler) Live(w http.ResponseWriter, _ *http.Request) {
	resp := dto.HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Service:   h.service,
		Version:   h.version,
	}

	_ = dto.WriteJSON(w, http.StatusOK, resp)
}

// Ready handles GET /health/ready
func (h *HealthHandler) Ready(w http.ResponseWriter, _ *http.Request) {
	// In a real implementation, this would check:
	// - Database connectivity
	// - Provider health
	// - Cache availability
	// etc.

	resp := dto.HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Service:   h.service,
		Version:   h.version,
	}

	_ = dto.WriteJSON(w, http.StatusOK, resp)
}
