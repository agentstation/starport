package controllers

import (
	"net/http"
	"time"

	"github.com/agentstation/starport/internal/server/dto"
)

// HealthController handles health check endpoints
type HealthController struct {
	version string
	service string
}

// NewHealthController creates a new health controller
func NewHealthController(service, version string) *HealthController {
	return &HealthController{
		service: service,
		version: version,
	}
}

// Live handles GET /health/live
func (h *HealthController) Live(w http.ResponseWriter, _ *http.Request) {
	resp := dto.HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Service:   h.service,
		Version:   h.version,
	}

	_ = dto.WriteJSON(w, http.StatusOK, resp)
}

// Ready handles GET /health/ready
func (h *HealthController) Ready(w http.ResponseWriter, _ *http.Request) {
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
