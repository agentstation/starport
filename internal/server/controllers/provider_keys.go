package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/server/dto"
)

// ProviderKeysController handles provider key management endpoints
type ProviderKeysController struct {
	providerKeys byok.ProviderKeys
}

// NewProviderKeysController creates a new provider keys controller
func NewProviderKeysController(providerKeys byok.ProviderKeys) *ProviderKeysController {
	return &ProviderKeysController{
		providerKeys: providerKeys,
	}
}

func (h *ProviderKeysController) requireProviderKeys(w http.ResponseWriter) bool {
	if h.providerKeys != nil {
		return true
	}

	dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, "Provider key management is not configured")
	return false
}

// List handles GET /api/v1/keys/{key_id}/provider-keys
func (h *ProviderKeysController) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireProviderKeys(w) {
		return
	}

	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")

	keys, err := h.providerKeys.ListKeys(ctx, "user:"+apiKeyID)
	if err != nil {
		log.Error().Err(err).Str("api_key_id", apiKeyID).Msg("Failed to list provider keys")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list provider keys")
		return
	}

	// Don't expose decrypted data in list view
	type keySummary struct {
		Provider   string         `json:"provider"`
		Config     map[string]any `json:"config,omitempty"`
		IsFallback bool           `json:"is_fallback"`
		Priority   int            `json:"priority"`
		CreatedAt  string         `json:"created_at"`
		LastUsed   *string        `json:"last_used,omitempty"`
		UsageCount int64          `json:"usage_count"`
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

	response := map[string]any{
		"provider_keys": summaries,
		"count":         len(summaries),
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Create handles POST /api/v1/keys/{key_id}/provider-keys
func (h *ProviderKeysController) Create(w http.ResponseWriter, r *http.Request) {
	if !h.requireProviderKeys(w) {
		return
	}

	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")

	var req struct {
		Provider    string         `json:"provider"`
		Credentials map[string]any `json:"credentials"`
		Config      map[string]any `json:"config,omitempty"`
		IsFallback  bool           `json:"is_fallback"`
		Priority    int            `json:"priority"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	// Validate provider
	if req.Provider == "" {
		dto.WriteValidationError(w, "provider", "Provider is required")
		return
	}

	// Validate credentials
	if len(req.Credentials) == 0 {
		dto.WriteValidationError(w, "credentials", "Credentials are required")
		return
	}

	// Convert credentials to string map
	credMap := make(map[string]string)
	for k, v := range req.Credentials {
		if strVal, ok := v.(string); ok {
			credMap[k] = strVal
		} else {
			dto.WriteValidationError(w, "credentials."+k, "Credential values must be strings")
			return
		}
	}

	// Validate the credentials with the provider if requested
	if validateParam := r.URL.Query().Get("validate"); validateParam == "true" {
		if err := h.providerKeys.ValidateKey(ctx, req.Provider, credMap, req.Config); err != nil {
			log.Warn().Err(err).Str("provider", req.Provider).Msg("Provider key validation failed")
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid credentials for provider")
			return
		}
	}

	// Store the key
	_, err := h.providerKeys.AddKey(ctx, "user:"+apiKeyID, req.Provider, credMap, req.Config, req.IsFallback, req.Priority)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest, "Provider key already exists")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", req.Provider).Msg("Failed to store provider key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to store provider key")
		return
	}

	response := map[string]any{
		"message":  "Provider key added successfully",
		"provider": req.Provider,
	}

	if err := dto.WriteJSON(w, http.StatusCreated, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Get handles GET /api/v1/keys/{key_id}/provider-keys/{provider}
func (h *ProviderKeysController) Get(w http.ResponseWriter, r *http.Request) {
	if !h.requireProviderKeys(w) {
		return
	}

	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	key, err := h.providerKeys.GetKey(ctx, "user:"+apiKeyID, provider)
	if err != nil {
		if errors.Is(err, byok.ErrKeyNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Provider key not found")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to get provider key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get provider key")
		return
	}

	// Don't expose actual credentials in response
	response := map[string]any{
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

	// Credentials are encrypted, we can't show fields
	response["has_credentials"] = key.EncryptedCredential != ""

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Update handles PUT /api/v1/keys/{key_id}/provider-keys/{provider}
func (h *ProviderKeysController) Update(w http.ResponseWriter, r *http.Request) {
	if !h.requireProviderKeys(w) {
		return
	}

	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	var req struct {
		Credentials map[string]any `json:"credentials,omitempty"`
		Config      map[string]any `json:"config,omitempty"`
		IsFallback  *bool          `json:"is_fallback,omitempty"`
		Priority    *int           `json:"priority,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	// Check if key exists
	_, err := h.providerKeys.GetKey(ctx, "user:"+apiKeyID, provider)
	if err != nil {
		if errors.Is(err, byok.ErrKeyNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Provider key not found")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to get provider key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get provider key")
		return
	}

	// Convert credentials to string map if provided
	var credMap map[string]string
	if len(req.Credentials) > 0 {
		credMap = make(map[string]string)
		for k, v := range req.Credentials {
			if strVal, ok := v.(string); ok {
				credMap[k] = strVal
			} else {
				dto.WriteValidationError(w, "credentials."+k, "Credential values must be strings")
				return
			}
		}
	}

	// Validate if credentials were updated and validation requested
	if credMap != nil && r.URL.Query().Get("validate") == "true" {
		if err := h.providerKeys.ValidateKey(ctx, provider, credMap, req.Config); err != nil {
			log.Warn().Err(err).Str("provider", provider).Msg("Provider key validation failed")
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid credentials for provider")
			return
		}
	}

	// Update the key
	_, err = h.providerKeys.UpdateKey(ctx, "user:"+apiKeyID, provider, credMap, req.Config, req.IsFallback, req.Priority)
	if err != nil {
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to update provider key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to update provider key")
		return
	}

	response := map[string]any{
		"message":  "Provider key updated successfully",
		"provider": provider,
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Delete handles DELETE /api/v1/keys/{key_id}/provider-keys/{provider}
func (h *ProviderKeysController) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.requireProviderKeys(w) {
		return
	}

	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	if err := h.providerKeys.DeleteKey(ctx, "user:"+apiKeyID, provider); err != nil {
		if errors.Is(err, byok.ErrKeyNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Provider key not found")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to delete provider key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to delete provider key")
		return
	}

	response := map[string]any{
		"message":  "Provider key deleted successfully",
		"provider": provider,
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Validate handles POST /api/v1/keys/{key_id}/provider-keys/{provider}/validate
func (h *ProviderKeysController) Validate(w http.ResponseWriter, r *http.Request) {
	if !h.requireProviderKeys(w) {
		return
	}

	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	provider := chi.URLParam(r, "provider")

	_, err := h.providerKeys.GetKey(ctx, "user:"+apiKeyID, provider)
	if err != nil {
		if errors.Is(err, byok.ErrKeyNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Provider key not found")
			return
		}
		log.Error().Err(err).Str("api_key_id", apiKeyID).Str("provider", provider).Msg("Failed to get provider key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get provider key")
		return
	}

	// We can't validate the key without decrypting credentials
	// For now, just return that the key exists
	response := map[string]any{
		"valid":   true,
		"message": "Provider key exists",
		"note":    "Validation requires decrypted credentials",
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// GetUsage handles GET /api/v1/keys/{key_id}/usage/provider-keys
func (h *ProviderKeysController) GetUsage(w http.ResponseWriter, _ *http.Request) {
	// Placeholder implementation
	response := map[string]any{
		"message": "Provider key usage analytics not yet implemented",
	}

	if err := dto.WriteJSON(w, http.StatusNotImplemented, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// GetUsageComparison handles GET /api/v1/keys/{key_id}/usage/comparison
func (h *ProviderKeysController) GetUsageComparison(w http.ResponseWriter, _ *http.Request) {
	// Placeholder implementation
	response := map[string]any{
		"message": "Usage comparison not yet implemented",
	}

	if err := dto.WriteJSON(w, http.StatusNotImplemented, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}
