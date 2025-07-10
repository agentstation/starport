package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// ProviderKeysHandler handles provider key management endpoints
type ProviderKeysHandler struct {
	manager providers.KeyManager
}

// NewProviderKeysHandler creates a new provider keys handler
func NewProviderKeysHandler(manager providers.KeyManager) *ProviderKeysHandler {
	return &ProviderKeysHandler{
		manager: manager,
	}
}

// RegisterRoutes registers provider key routes on the router
func (h *ProviderKeysHandler) RegisterRoutes(r chi.Router) {
	// User endpoints for managing their own provider keys
	r.Route("/api/v1/keys/{key_id}/provider-keys", func(r chi.Router) {
		r.Use(requireAPIKey)
		r.Use(validateKeyOwnership)
		
		r.Get("/", h.ListProviderKeys)
		r.Post("/", h.AddProviderKey)
		r.Get("/{provider}", h.GetProviderKey)
		r.Put("/{provider}", h.UpdateProviderKey)
		r.Delete("/{provider}", h.DeleteProviderKey)
		r.Post("/{provider}/validate", h.ValidateProviderKey)
	})

	// Admin endpoints for managing global provider keys
	r.Route("/api/v1/admin/global-provider-keys", func(r chi.Router) {
		r.Use(requireAdminAuth)
		
		r.Get("/", h.ListGlobalProviderKeys)
		r.Post("/", h.SetGlobalProviderKey)
		r.Get("/{provider}", h.GetGlobalProviderKey)
		r.Delete("/{provider}", h.DeleteGlobalProviderKey)
	})

	// Usage analytics endpoints
	r.Route("/api/v1/keys/{key_id}/usage", func(r chi.Router) {
		r.Use(requireAPIKey)
		r.Use(validateKeyOwnership)
		
		r.Get("/provider-keys", h.GetProviderKeyUsage)
		r.Get("/comparison", h.GetUsageComparison)
	})
}

// ListProviderKeys lists all provider keys for an API key
func (h *ProviderKeysHandler) ListProviderKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")

	keys, err := h.manager.ListKeys(ctx, "user:"+apiKeyID)
	if err != nil {
		log.Error().Err(err).Str("api_key_id", apiKeyID).Msg("Failed to list provider keys")
		writeError(w, http.StatusInternalServerError, "Failed to list provider keys")
		return
	}

	// Don't expose decrypted data in list view
	type keySummary struct {
		Provider   string                 `json:"provider"`
		Config     map[string]interface{} `json:"config,omitempty"`
		IsFallback bool                   `json:"is_fallback"`
		Priority   int                    `json:"priority"`
		CreatedAt  string                 `json:"created_at"`
		LastUsed   *string                `json:"last_used,omitempty"`
		UsageCount int64                  `json:"usage_count"`
	}

	summaries := make([]keySummary, len(keys))
	for i, key := range keys {
		summary := keySummary{
			Provider:   key.Provider,
			Config:     key.Config,
			IsFallback: key.IsFallback,
			Priority:   key.Priority,
			CreatedAt:  key.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UsageCount: key.UsageCount,
		}
		if key.LastUsed != nil {
			lastUsed := key.LastUsed.Format("2006-01-02T15:04:05Z")
			summary.LastUsed = &lastUsed
		}
		summaries[i] = summary
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"provider_keys": summaries,
	})
}

// AddProviderKey adds a new provider key
func (h *ProviderKeysHandler) AddProviderKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")

	var req struct {
		Provider string                 `json:"provider"`
		Key      map[string]string      `json:"key"`
		Config   map[string]interface{} `json:"config,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "Provider is required")
		return
	}

	if len(req.Key) == 0 {
		writeError(w, http.StatusBadRequest, "Key is required")
		return
	}

	// Add provider key
	_, err := h.manager.AddKey(ctx, "user:"+apiKeyID, req.Provider, req.Key, req.Config, false, 0)
	if err != nil {
		var valErr *providers.ValidationError
		if errors.As(err, &valErr) {
			writeError(w, http.StatusBadRequest, valErr.Error())
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", req.Provider).Msg("Failed to add provider key")
		writeError(w, http.StatusInternalServerError, "Failed to add provider key")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "Provider key added successfully",
		"provider": req.Provider,
	})
}

// GetProviderKey retrieves a specific provider key
func (h *ProviderKeysHandler) GetProviderKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	key, err := h.manager.GetKey(ctx, "user:"+apiKeyID, provider)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Provider key not found")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to get provider key")
		writeError(w, http.StatusInternalServerError, "Failed to get provider key")
		return
	}

	// Don't expose the actual key data
	response := map[string]interface{}{
		"provider":    key.Provider,
		"config":      key.Config,
		"is_fallback": key.IsFallback,
		"priority":    key.Priority,
		"created_at":  key.CreatedAt.Format("2006-01-02T15:04:05Z"),
		"usage_count": key.UsageCount,
	}
	if key.LastUsed != nil {
		response["last_used"] = key.LastUsed.Format("2006-01-02T15:04:05Z")
	}

	writeJSON(w, http.StatusOK, response)
}

// UpdateProviderKey updates an existing provider key
func (h *ProviderKeysHandler) UpdateProviderKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	var req struct {
		Key        map[string]string      `json:"key,omitempty"`
		Config     map[string]interface{} `json:"config,omitempty"`
		IsFallback *bool                  `json:"is_fallback,omitempty"`
		Priority   *int                   `json:"priority,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Update provider key
	_, err := h.manager.UpdateKey(ctx, "user:"+apiKeyID, provider, req.Key, req.Config, req.IsFallback, req.Priority)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "Provider key not found")
			return
		}
		var valErr *providers.ValidationError
		if errors.As(err, &valErr) {
			writeError(w, http.StatusBadRequest, valErr.Error())
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to update provider key")
		writeError(w, http.StatusInternalServerError, "Failed to update provider key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Provider key updated successfully",
		"provider": provider,
	})
}

// DeleteProviderKey removes a provider key
func (h *ProviderKeysHandler) DeleteProviderKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	err := h.manager.DeleteKey(ctx, "user:"+apiKeyID, provider)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "Provider key not found")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to delete provider key")
		writeError(w, http.StatusInternalServerError, "Failed to delete provider key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Provider key deleted successfully",
		"provider": provider,
	})
}

// ValidateProviderKey validates a provider key without storing it
func (h *ProviderKeysHandler) ValidateProviderKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provider := chi.URLParam(r, "provider")

	var req struct {
		Key    map[string]string      `json:"key"`
		Config map[string]interface{} `json:"config,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if len(req.Key) == 0 {
		writeError(w, http.StatusBadRequest, "Key is required")
		return
	}

	// Validate provider key
	err := h.manager.ValidateKey(ctx, provider, req.Key, req.Config)
	if err != nil {
		var valErr *providers.ValidationError
		if errors.As(err, &valErr) {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"valid":   false,
				"error":   valErr.Error(),
				"field":   valErr.Field,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"valid": false,
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid": true,
		"provider": provider,
	})
}

// Admin endpoints

// ListGlobalProviderKeys lists all global provider keys
func (h *ProviderKeysHandler) ListGlobalProviderKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	keys, err := h.manager.ListGlobalKeys(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list global provider keys")
		writeError(w, http.StatusInternalServerError, "Failed to list global provider keys")
		return
	}

	type keySummary struct {
		Provider   string                  `json:"provider"`
		Config     map[string]interface{}  `json:"config,omitempty"`
		RateLimit  *models.RateLimitConfig `json:"rate_limit,omitempty"`
		CreatedAt  string                  `json:"created_at"`
	}

	summaries := make([]keySummary, len(keys))
	for i, key := range keys {
		summaries[i] = keySummary{
			Provider:  key.Provider,
			Config:    key.Config,
			RateLimit: key.RateLimit,
			CreatedAt: key.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"global_provider_keys": summaries,
	})
}

// SetGlobalProviderKey sets a global provider key
func (h *ProviderKeysHandler) SetGlobalProviderKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Provider   string                  `json:"provider"`
		Key        map[string]string       `json:"key"`
		Config     map[string]interface{}  `json:"config,omitempty"`
		RateLimit  *models.RateLimitConfig `json:"rate_limit,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "Provider is required")
		return
	}

	if len(req.Key) == 0 {
		writeError(w, http.StatusBadRequest, "Key is required")
		return
	}

	// Set global provider key
	_, err := h.manager.AddGlobalKey(ctx, req.Provider, req.Key, req.Config, req.RateLimit)
	if err != nil {
		var valErr *providers.ValidationError
		if errors.As(err, &valErr) {
			writeError(w, http.StatusBadRequest, valErr.Error())
			return
		}
		log.Error().Err(err).Str("provider", req.Provider).Msg("Failed to set global provider key")
		writeError(w, http.StatusInternalServerError, "Failed to set global provider key")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message":  "Global provider key set successfully",
		"provider": req.Provider,
	})
}

// GetGlobalProviderKey retrieves a global provider key
func (h *ProviderKeysHandler) GetGlobalProviderKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provider := chi.URLParam(r, "provider")

	key, err := h.manager.GetGlobalKey(ctx, provider)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Global provider key not found")
			return
		}
		log.Error().Err(err).Str("provider", provider).Msg("Failed to get global provider key")
		writeError(w, http.StatusInternalServerError, "Failed to get global provider key")
		return
	}

	// Don't expose the actual key data
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"provider":   key.Provider,
		"config":     key.Config,
		"rate_limit": key.RateLimit,
		"created_at": key.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// DeleteGlobalProviderKey removes a global provider key
func (h *ProviderKeysHandler) DeleteGlobalProviderKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provider := chi.URLParam(r, "provider")

	err := h.manager.DeleteGlobalKey(ctx, provider)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "Global provider key not found")
			return
		}
		log.Error().Err(err).Str("provider", provider).Msg("Failed to delete global provider key")
		writeError(w, http.StatusInternalServerError, "Failed to delete global provider key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":  "Global provider key deleted successfully",
		"provider": provider,
	})
}

// Usage analytics endpoints

// GetProviderKeyUsage returns provider key usage statistics
func (h *ProviderKeysHandler) GetProviderKeyUsage(w http.ResponseWriter, _ *http.Request) {
	// This would be implemented with actual usage tracking
	// For now, return a placeholder response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"usage": map[string]interface{}{
			"total_requests":     0,
			"total_tokens":       0,
			"total_cost":         0.0,
			"providers":          map[string]interface{}{},
			"period_start":       "2024-01-01T00:00:00Z",
			"period_end":         "2024-01-31T23:59:59Z",
		},
	})
}

// GetUsageComparison returns a comparison of provider key vs gateway usage
func (h *ProviderKeysHandler) GetUsageComparison(w http.ResponseWriter, _ *http.Request) {
	// This would be implemented with actual usage tracking
	// For now, return a placeholder response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"comparison": map[string]interface{}{
			"provider_keys": map[string]interface{}{
				"requests":     0,
				"tokens":       0,
				"cost":         0.0,
				"savings":      0.0,
			},
			"gateway": map[string]interface{}{
				"requests":     0,
				"tokens":       0,
				"cost":         0.0,
			},
			"total_savings": 0.0,
			"period_start":  "2024-01-01T00:00:00Z",
			"period_end":    "2024-01-31T23:59:59Z",
		},
	})
}

// Helper functions

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Error().Err(err).Msg("Failed to encode JSON response")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]interface{}{
		"error": message,
	})
}

// Middleware placeholders - these would be implemented elsewhere

func requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: Implement API key authentication
		next.ServeHTTP(w, r)
	})
}

func validateKeyOwnership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: Validate that the authenticated user owns the API key
		next.ServeHTTP(w, r)
	})
}

func requireAdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: Implement admin authentication
		next.ServeHTTP(w, r)
	})
}