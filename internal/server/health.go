package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/rs/zerolog/log"
)

// HealthResponse represents a health check response
type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp string            `json:"timestamp"`
	Version   string            `json:"version,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
}

// handleLive handles the liveness probe endpoint
// This endpoint indicates whether the application is running
func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	response := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("failed to encode health response")
	}
}

// handleReady handles the readiness probe endpoint
// This endpoint indicates whether the application is ready to serve requests
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	// In the future, this will check:
	// - Database connectivity
	// - Cache connectivity
	// - Required services availability
	
	// For now, we just check if the server is running
	response := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Version:   "dev", // This will be injected during build
		Details: map[string]string{
			"go_version": runtime.Version(),
			"goroutines": fmt.Sprintf("%d", runtime.NumGoroutine()),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("failed to encode ready response")
	}
}