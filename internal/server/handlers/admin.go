package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/storage"
)

// AdminHandler handles administrative endpoints
type AdminHandler struct {
	store storage.KVStore
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(store storage.KVStore) *AdminHandler {
	return &AdminHandler{
		store: store,
	}
}

// ListKeys handles GET /api/v1/admin/keys
func (h *AdminHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get pagination parameters
	limit := 100
	offset := 0
	// TODO: Parse from query params

	// List API keys from storage
	prefix := "apikey:"
	keyNames, err := h.store.ScanWithPrefix(ctx, prefix, limit)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list API keys")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list API keys")
		return
	}

	// Get and deserialize keys
	apiKeys := make([]models.APIKey, 0, len(keyNames))
	for _, keyName := range keyNames {
		data, err := h.store.Get(ctx, keyName)
		if err != nil {
			continue // Skip on error
		}

		var apiKey models.APIKey
		if err := storage.Deserialize(data, &apiKey); err != nil {
			log.Error().Err(err).Msg("Failed to deserialize API key")
			continue
		}
		apiKeys = append(apiKeys, apiKey)
	}

	response := map[string]interface{}{
		"keys":  apiKeys,
		"count": len(apiKeys),
		"pagination": map[string]interface{}{
			"limit":  limit,
			"offset": offset,
		},
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// CreateKey handles POST /api/v1/admin/keys
func (h *AdminHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Name        string                  `json:"name"`
		Description string                  `json:"description,omitempty"`
		Scopes      []string                `json:"scopes,omitempty"`
		RateLimit   *models.RateLimitConfig `json:"rate_limit,omitempty"`
		Metadata    map[string]string       `json:"metadata,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	// Create API key
	apiKey := &models.APIKey{
		Name:      req.Name,
		Scopes:    req.Scopes,
		Metadata:  convertStringMapToInterface(req.Metadata),
		Active:    true,
		CreatedAt: time.Now(),
	}

	// Generate key ID and hash
	apiKey.ID = generateAPIKeyID()
	// TODO: Generate actual key and hash
	apiKey.Hash = "placeholder-hash"
	key := "sk_" + generateRandomString(32) // The actual key shown to user once

	// Basic validation
	if apiKey.Name == "" {
		dto.WriteValidationError(w, "name", "Name is required")
		return
	}

	// Store
	keyData, err := storage.Serialize(apiKey)
	if err != nil {
		log.Error().Err(err).Msg("Failed to serialize API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to create API key")
		return
	}

	if err := h.store.Set(ctx, storage.APIKeyKey(apiKey.ID), keyData); err != nil {
		log.Error().Err(err).Msg("Failed to store API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to create API key")
		return
	}

	// Return created key (with the actual key value visible once)
	response := map[string]interface{}{
		"key": map[string]interface{}{
			"id":         apiKey.ID,
			"key":        key, // Only shown on creation
			"name":       apiKey.Name,
			"scopes":     apiKey.Scopes,
			"active":     apiKey.Active,
			"created_at": apiKey.CreatedAt,
		},
		"message": "API key created successfully. Save the key value as it won't be shown again.",
	}

	if err := dto.WriteJSON(w, http.StatusCreated, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// GetKey handles GET /api/v1/admin/keys/{key_id}
func (h *AdminHandler) GetKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	// Get key from storage
	keyData, err := h.store.Get(ctx, storage.APIKeyKey(keyID))
	if err != nil {
		if err == storage.ErrNotFound {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to get API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get API key")
		return
	}

	// Deserialize
	var apiKey models.APIKey
	if err := storage.Deserialize(keyData, &apiKey); err != nil {
		log.Error().Err(err).Msg("Failed to deserialize API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get API key")
		return
	}

	// Don't expose the hash
	apiKey.Hash = ""

	if err := dto.WriteJSON(w, http.StatusOK, apiKey); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// UpdateKey handles PUT /api/v1/admin/keys/{key_id}
func (h *AdminHandler) UpdateKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	// Get existing key
	keyData, err := h.store.Get(ctx, storage.APIKeyKey(keyID))
	if err != nil {
		if err == storage.ErrNotFound {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to get API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get API key")
		return
	}

	var apiKey models.APIKey
	if err := storage.Deserialize(keyData, &apiKey); err != nil {
		log.Error().Err(err).Msg("Failed to deserialize API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get API key")
		return
	}

	// Parse update request
	var req struct {
		Name     *string           `json:"name,omitempty"`
		Scopes   []string          `json:"scopes,omitempty"`
		Metadata map[string]string `json:"metadata,omitempty"`
		Active   *bool             `json:"active,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	// Update fields
	if req.Name != nil {
		apiKey.Name = *req.Name
	}
	if req.Scopes != nil {
		apiKey.Scopes = req.Scopes
	}
	if req.Metadata != nil {
		apiKey.Metadata = convertStringMapToInterface(req.Metadata)
	}
	if req.Active != nil {
		apiKey.Active = *req.Active
	}

	// Basic validation
	if apiKey.Name == "" {
		dto.WriteValidationError(w, "name", "Name cannot be empty")
		return
	}

	// Store updated key
	updatedData, err := storage.Serialize(&apiKey)
	if err != nil {
		log.Error().Err(err).Msg("Failed to serialize API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to update API key")
		return
	}

	if err := h.store.Set(ctx, storage.APIKeyKey(keyID), updatedData); err != nil {
		log.Error().Err(err).Msg("Failed to store API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to update API key")
		return
	}

	// Don't expose the hash
	apiKey.Hash = ""

	if err := dto.WriteJSON(w, http.StatusOK, apiKey); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// DeleteKey handles DELETE /api/v1/admin/keys/{key_id}
func (h *AdminHandler) DeleteKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	if err := h.store.Delete(ctx, storage.APIKeyKey(keyID)); err != nil {
		if err == storage.ErrNotFound {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to delete API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to delete API key")
		return
	}

	response := map[string]interface{}{
		"message": "API key deleted successfully",
		"key_id":  keyID,
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// SystemInfo handles GET /api/v1/admin/info
func (h *AdminHandler) SystemInfo(w http.ResponseWriter, _ *http.Request) {
	// TODO: Implement actual system info gathering
	info := map[string]interface{}{
		"service":    "starport",
		"version":    "1.0.0",
		"uptime":     "TODO",
		"go_version": "TODO",
		"os":         "TODO",
		"arch":       "TODO",
		"storage": map[string]interface{}{
			"type":   "badger",
			"status": "healthy",
		},
		"providers": map[string]interface{}{
			"count":  "TODO",
			"status": "TODO",
		},
	}

	if err := dto.WriteJSON(w, http.StatusOK, info); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Metrics handles GET /api/v1/admin/metrics
func (h *AdminHandler) Metrics(w http.ResponseWriter, _ *http.Request) {
	// TODO: Implement actual metrics gathering
	metrics := map[string]interface{}{
		"requests": map[string]interface{}{
			"total":     0,
			"success":   0,
			"errors":    0,
			"rate_1min": 0,
		},
		"latency": map[string]interface{}{
			"p50": 0,
			"p95": 0,
			"p99": 0,
		},
		"providers": map[string]interface{}{
			// Provider-specific metrics
		},
	}

	if err := dto.WriteJSON(w, http.StatusOK, metrics); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Helper functions

func convertStringMapToInterface(m map[string]string) map[string]interface{} {
	if m == nil {
		return nil
	}
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

func generateAPIKeyID() string {
	// Simple ID generation - in production, use a proper UUID
	return fmt.Sprintf("sk_%d", time.Now().UnixNano())
}

func generateRandomString(length int) string {
	// Simple random string generation - in production, use crypto/rand
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
