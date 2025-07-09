package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/agentstation/starport/internal/byok"
	"github.com/agentstation/starport/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// BYOKHandler handles BYOK credential management endpoints
type BYOKHandler struct {
	manager byok.Manager
}

// NewBYOKHandler creates a new BYOK handler
func NewBYOKHandler(manager byok.Manager) *BYOKHandler {
	return &BYOKHandler{
		manager: manager,
	}
}

// RegisterRoutes registers BYOK routes on the router
func (h *BYOKHandler) RegisterRoutes(r chi.Router) {
	// User endpoints for managing their own BYOK credentials
	r.Route("/api/v1/keys/{key_id}/credentials", func(r chi.Router) {
		r.Use(requireAPIKey)
		r.Use(validateKeyOwnership)
		
		r.Get("/", h.ListCredentials)
		r.Post("/", h.AddCredential)
		r.Get("/{provider}", h.GetCredential)
		r.Put("/{provider}", h.UpdateCredential)
		r.Delete("/{provider}", h.DeleteCredential)
		r.Post("/{provider}/validate", h.ValidateCredential)
	})

	// Admin endpoints for managing default keys
	r.Route("/api/v1/admin/default-keys", func(r chi.Router) {
		r.Use(requireAdminAuth)
		
		r.Get("/", h.ListDefaultKeys)
		r.Post("/", h.SetDefaultKey)
		r.Get("/{provider}", h.GetDefaultKey)
		r.Delete("/{provider}", h.DeleteDefaultKey)
	})

	// Usage analytics endpoints
	r.Route("/api/v1/keys/{key_id}/usage", func(r chi.Router) {
		r.Use(requireAPIKey)
		r.Use(validateKeyOwnership)
		
		r.Get("/byok", h.GetBYOKUsage)
		r.Get("/comparison", h.GetUsageComparison)
	})
}

// ListCredentials lists all BYOK credentials for an API key
func (h *BYOKHandler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")

	credentials, err := h.manager.ListCredentials(ctx, apiKeyID)
	if err != nil {
		log.Error().Err(err).Str("api_key_id", apiKeyID).Msg("Failed to list credentials")
		writeError(w, http.StatusInternalServerError, "Failed to list credentials")
		return
	}

	// Don't expose decrypted data in list view
	type credentialSummary struct {
		Provider   string                 `json:"provider"`
		Config     map[string]interface{} `json:"config,omitempty"`
		IsFallback bool                   `json:"is_fallback"`
		Priority   int                    `json:"priority"`
		CreatedAt  string                 `json:"created_at"`
		LastUsed   *string                `json:"last_used,omitempty"`
		UsageCount int64                  `json:"usage_count"`
	}

	summaries := make([]credentialSummary, len(credentials))
	for i, cred := range credentials {
		summary := credentialSummary{
			Provider:   cred.Provider,
			Config:     cred.Config,
			IsFallback: cred.IsFallback,
			Priority:   cred.Priority,
			CreatedAt:  cred.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UsageCount: cred.UsageCount,
		}
		if cred.LastUsed != nil {
			lastUsed := cred.LastUsed.Format("2006-01-02T15:04:05Z")
			summary.LastUsed = &lastUsed
		}
		summaries[i] = summary
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"credentials": summaries,
	})
}

// AddCredential adds a new BYOK credential
func (h *BYOKHandler) AddCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")

	var req struct {
		Provider string                 `json:"provider"`
		Credential map[string]string   `json:"credential"`
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

	if len(req.Credential) == 0 {
		writeError(w, http.StatusBadRequest, "Credential is required")
		return
	}

	// Add credential
	err := h.manager.AddCredential(ctx, apiKeyID, req.Provider, req.Credential, req.Config)
	if err != nil {
		var valErr *byok.ValidationError
		if errors.As(err, &valErr) {
			writeError(w, http.StatusBadRequest, valErr.Error())
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", req.Provider).Msg("Failed to add credential")
		writeError(w, http.StatusInternalServerError, "Failed to add credential")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "Credential added successfully",
		"provider": req.Provider,
	})
}

// GetCredential retrieves a specific BYOK credential
func (h *BYOKHandler) GetCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	credential, err := h.manager.GetCredential(ctx, apiKeyID, provider)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Credential not found")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to get credential")
		writeError(w, http.StatusInternalServerError, "Failed to get credential")
		return
	}

	// Don't expose the actual credential data
	response := map[string]interface{}{
		"provider":    credential.Provider,
		"config":      credential.Config,
		"is_fallback": credential.IsFallback,
		"priority":    credential.Priority,
		"created_at":  credential.CreatedAt.Format("2006-01-02T15:04:05Z"),
		"usage_count": credential.UsageCount,
	}
	if credential.LastUsed != nil {
		response["last_used"] = credential.LastUsed.Format("2006-01-02T15:04:05Z")
	}

	writeJSON(w, http.StatusOK, response)
}

// UpdateCredential updates an existing BYOK credential
func (h *BYOKHandler) UpdateCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	var req struct {
		Credential map[string]string      `json:"credential,omitempty"`
		Config     map[string]interface{} `json:"config,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Update credential
	err := h.manager.UpdateCredential(ctx, apiKeyID, provider, req.Credential, req.Config)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "Credential not found")
			return
		}
		var valErr *byok.ValidationError
		if errors.As(err, &valErr) {
			writeError(w, http.StatusBadRequest, valErr.Error())
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to update credential")
		writeError(w, http.StatusInternalServerError, "Failed to update credential")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Credential updated successfully",
		"provider": provider,
	})
}

// DeleteCredential removes a BYOK credential
func (h *BYOKHandler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	err := h.manager.DeleteCredential(ctx, apiKeyID, provider)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "Credential not found")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to delete credential")
		writeError(w, http.StatusInternalServerError, "Failed to delete credential")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Credential deleted successfully",
		"provider": provider,
	})
}

// ValidateCredential validates a BYOK credential without storing it
func (h *BYOKHandler) ValidateCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provider := chi.URLParam(r, "provider")

	var req struct {
		Credential map[string]string      `json:"credential"`
		Config     map[string]interface{} `json:"config,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if len(req.Credential) == 0 {
		writeError(w, http.StatusBadRequest, "Credential is required")
		return
	}

	// Validate credential
	err := h.manager.ValidateCredential(ctx, provider, req.Credential, req.Config)
	if err != nil {
		var valErr *byok.ValidationError
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

// ListDefaultKeys lists all default provider keys
func (h *BYOKHandler) ListDefaultKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	keys, err := h.manager.ListDefaultKeys(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list default keys")
		writeError(w, http.StatusInternalServerError, "Failed to list default keys")
		return
	}

	type keySummary struct {
		Provider  string                 `json:"provider"`
		Config    map[string]interface{} `json:"config,omitempty"`
		CreatedAt string                 `json:"created_at"`
	}

	summaries := make([]keySummary, len(keys))
	for i, key := range keys {
		summaries[i] = keySummary{
			Provider:  key.Provider,
			Config:    key.Config,
			CreatedAt: key.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"default_keys": summaries,
	})
}

// SetDefaultKey sets a default provider key
func (h *BYOKHandler) SetDefaultKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Provider   string                 `json:"provider"`
		Credential map[string]string      `json:"credential"`
		Config     map[string]interface{} `json:"config,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "Provider is required")
		return
	}

	if len(req.Credential) == 0 {
		writeError(w, http.StatusBadRequest, "Credential is required")
		return
	}

	// Set default key
	err := h.manager.SetDefaultKey(ctx, req.Provider, req.Credential, req.Config)
	if err != nil {
		var valErr *byok.ValidationError
		if errors.As(err, &valErr) {
			writeError(w, http.StatusBadRequest, valErr.Error())
			return
		}
		log.Error().Err(err).Str("provider", req.Provider).Msg("Failed to set default key")
		writeError(w, http.StatusInternalServerError, "Failed to set default key")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message":  "Default key set successfully",
		"provider": req.Provider,
	})
}

// GetDefaultKey retrieves a default provider key
func (h *BYOKHandler) GetDefaultKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provider := chi.URLParam(r, "provider")

	key, err := h.manager.GetDefaultKey(ctx, provider)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Default key not found")
			return
		}
		log.Error().Err(err).Str("provider", provider).Msg("Failed to get default key")
		writeError(w, http.StatusInternalServerError, "Failed to get default key")
		return
	}

	// Don't expose the actual credential data
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"provider":   key.Provider,
		"config":     key.Config,
		"created_at": key.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// DeleteDefaultKey removes a default provider key
func (h *BYOKHandler) DeleteDefaultKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provider := chi.URLParam(r, "provider")

	err := h.manager.DeleteDefaultKey(ctx, provider)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "Default key not found")
			return
		}
		log.Error().Err(err).Str("provider", provider).Msg("Failed to delete default key")
		writeError(w, http.StatusInternalServerError, "Failed to delete default key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":  "Default key deleted successfully",
		"provider": provider,
	})
}

// Usage analytics endpoints

// GetBYOKUsage returns BYOK usage statistics
func (h *BYOKHandler) GetBYOKUsage(w http.ResponseWriter, _ *http.Request) {
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

// GetUsageComparison returns a comparison of BYOK vs gateway usage
func (h *BYOKHandler) GetUsageComparison(w http.ResponseWriter, _ *http.Request) {
	// This would be implemented with actual usage tracking
	// For now, return a placeholder response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"comparison": map[string]interface{}{
			"byok": map[string]interface{}{
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