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
	ready   func() bool
}

// NewHealthController creates a new health controller
func NewHealthController(service, version string, readiness func() bool) *HealthController {
	return &HealthController{
		service: service,
		version: version,
		ready:   readiness,
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
	resp := dto.HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Service:   h.service,
		Version:   h.version,
	}

	status := http.StatusOK
	if h.ready != nil && !h.ready() {
		resp.Status = "not_ready"
		status = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "1")
	}
	_ = dto.WriteJSON(w, status, resp)
}
