package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"runtime"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server/dto"
)

const systemInfoUnavailable = "unavailable"

// AdminController handles administrative endpoints
type AdminController struct {
	identities identity.Repository
	issuer     *identity.Issuer
}

// NewAdminController creates a new admin controller
func NewAdminController(identities identity.Repository) *AdminController {
	issuer, _ := identity.NewIssuer(identities)
	return &AdminController{
		identities: identities,
		issuer:     issuer,
	}
}

// ListKeys handles GET /api/v1/admin/keys
func (h *AdminController) ListKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get pagination parameters
	limit := 100
	offset := 0
	// TODO: Parse from query params

	records, err := h.identities.List(ctx, limit)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list API keys")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list API keys")
		return
	}

	apiKeys := make([]identity.APIKey, 0, len(records))
	for _, record := range records {
		apiKey := record.APIKey
		apiKey.Hash = ""
		apiKeys = append(apiKeys, apiKey)
	}

	response := map[string]any{
		"keys":             apiKeys,
		responseCountField: len(apiKeys),
		"pagination": map[string]any{
			"limit":  limit,
			"offset": offset,
		},
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// CreateKey handles POST /api/v1/admin/keys
func (h *AdminController) CreateKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description,omitempty"`
		Scopes      []string          `json:"scopes,omitempty"`
		Metadata    map[string]string `json:"metadata,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	issued, err := h.issuer.Issue(ctx, identity.IssueRequest{
		Name:     req.Name,
		Scopes:   req.Scopes,
		Metadata: convertStringMapToInterface(req.Metadata),
	})
	if err != nil {
		if errors.Is(err, identity.ErrInvalidName) || errors.Is(err, identity.ErrMissingScopes) ||
			errors.Is(err, identity.ErrInvalidScope) {
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
			return
		}
		log.Error().Err(err).Msg("Failed to create API key identity")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to create API key")
		return
	}
	apiKey := issued.APIKey

	// Return created key (with the actual key value visible once)
	response := map[string]any{
		"key": map[string]any{
			"id":         apiKey.ID,
			"key":        issued.Secret,
			"name":       apiKey.Name,
			"scopes":     apiKey.Scopes,
			"active":     apiKey.Active,
			"created_at": apiKey.CreatedAt,
		},
		responseMessageField: "API key created successfully. Save the key value as it won't be shown again.",
	}

	if err := dto.WriteJSON(w, http.StatusCreated, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// GetKey handles GET /api/v1/admin/keys/{key_id}
func (h *AdminController) GetKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	record, err := h.identities.GetByID(ctx, keyID)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to get API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get API key")
		return
	}

	apiKey := record.APIKey
	apiKey.Hash = ""

	if err := dto.WriteJSON(w, http.StatusOK, apiKey); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// UpdateKey handles PUT /api/v1/admin/keys/{key_id}
func (h *AdminController) UpdateKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	record, err := h.identities.GetByID(ctx, keyID)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to get API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get API key")
		return
	}

	apiKey := record.APIKey

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

	if err := apiKey.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	updated, err := h.identities.Update(ctx, apiKey, record.Revision)
	if err != nil {
		if errors.Is(err, identity.ErrConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest, "API key changed during update")
			return
		}
		log.Error().Err(err).Msg("Failed to update API key identity")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to update API key")
		return
	}

	apiKey = updated.APIKey
	apiKey.Hash = ""

	if err := dto.WriteJSON(w, http.StatusOK, apiKey); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// DeleteKey handles DELETE /api/v1/admin/keys/{key_id}
func (h *AdminController) DeleteKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	if err := h.identities.Delete(ctx, keyID, 0); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to delete API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to delete API key")
		return
	}

	response := map[string]any{
		responseMessageField: "API key deleted successfully",
		"key_id":             keyID,
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// SystemInfo handles GET /api/v1/admin/info
func (h *AdminController) SystemInfo(w http.ResponseWriter, _ *http.Request) {
	// TODO: Implement actual system info gathering
	info := map[string]any{
		"service":    "starport",
		"version":    "1.0.0",
		"uptime":     systemInfoUnavailable,
		"go_version": runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"storage": map[string]any{
			"type":   "badger",
			"status": "healthy",
		},
		"providers": map[string]any{
			responseCountField: systemInfoUnavailable,
			"status":           systemInfoUnavailable,
		},
	}

	if err := dto.WriteJSON(w, http.StatusOK, info); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Metrics handles GET /api/v1/admin/metrics
func (h *AdminController) Metrics(w http.ResponseWriter, _ *http.Request) {
	// TODO: Implement actual metrics gathering
	metrics := map[string]any{
		"requests": map[string]any{
			"total":     0,
			"success":   0,
			"errors":    0,
			"rate_1min": 0,
		},
		"latency": map[string]any{
			"p50": 0,
			"p95": 0,
			"p99": 0,
		},
		"providers": map[string]any{
			// Provider-specific metrics
		},
	}

	if err := dto.WriteJSON(w, http.StatusOK, metrics); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// Helper functions

func convertStringMapToInterface(m map[string]string) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
