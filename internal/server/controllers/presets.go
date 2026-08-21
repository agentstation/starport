package controllers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/presets"
	"github.com/agentstation/starport/internal/server/dto"
)

const presetsNotConfiguredMessage = "Preset storage is not configured"

// PresetsController serves preset CRUD under /api/v1/presets.
type PresetsController struct {
	repository presets.Repository
}

// NewPresetsController creates the preset management adapter.
func NewPresetsController(repository presets.Repository) *PresetsController {
	return &PresetsController{repository: repository}
}

// presetPayload is the wire shape of one preset, in and out. Revision is
// server-assigned on responses and names the expected revision on updates.
type presetPayload struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Config      presets.Config `json:"config"`
	Revision    uint64         `json:"revision,omitempty"`
	CreatedAt   time.Time      `json:"created_at,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
}

func presetResponse(record presets.Record) presetPayload {
	return presetPayload{
		Name:        record.Preset.Name,
		Description: record.Preset.Description,
		Config:      record.Preset.Config,
		Revision:    record.Revision,
		CreatedAt:   record.Preset.CreatedAt,
		UpdatedAt:   record.Preset.UpdatedAt,
	}
}

// List handles GET /api/v1/presets.
func (h *PresetsController) List(w http.ResponseWriter, r *http.Request) {
	if h.repository == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, presetsNotConfiguredMessage)
		return
	}
	records, err := h.repository.List(r.Context(), 0)
	if err != nil {
		writePresetError(w, err)
		return
	}
	payloads := make([]presetPayload, 0, len(records))
	for _, record := range records {
		payloads = append(payloads, presetResponse(record))
	}
	writePresetJSON(w, http.StatusOK, map[string]any{"data": payloads})
}

// Get handles GET /api/v1/presets/{name}.
func (h *PresetsController) Get(w http.ResponseWriter, r *http.Request) {
	if h.repository == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, presetsNotConfiguredMessage)
		return
	}
	record, err := h.repository.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writePresetError(w, err)
		return
	}
	writePresetJSON(w, http.StatusOK, presetResponse(record))
}

// Create handles POST /api/v1/presets.
func (h *PresetsController) Create(w http.ResponseWriter, r *http.Request) {
	if h.repository == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, presetsNotConfiguredMessage)
		return
	}
	payload, ok := decodePresetPayload(w, r.Body)
	if !ok {
		return
	}
	now := time.Now().UTC()
	record, err := h.repository.Create(r.Context(), presets.Preset{
		Name:        payload.Name,
		Description: payload.Description,
		Config:      payload.Config,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		writePresetError(w, err)
		return
	}
	writePresetJSON(w, http.StatusCreated, presetResponse(record))
}

// Update handles PUT /api/v1/presets/{name}. The body's revision field names
// the revision the caller read; a mismatch conflicts.
func (h *PresetsController) Update(w http.ResponseWriter, r *http.Request) {
	if h.repository == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, presetsNotConfiguredMessage)
		return
	}
	name := chi.URLParam(r, "name")
	payload, ok := decodePresetPayload(w, r.Body)
	if !ok {
		return
	}
	if payload.Name != "" && payload.Name != name {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Preset name is immutable")
		return
	}
	if payload.Revision == 0 {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Update requires the expected revision")
		return
	}
	current, err := h.repository.Get(r.Context(), name)
	if err != nil {
		writePresetError(w, err)
		return
	}
	record, err := h.repository.Update(r.Context(), presets.Preset{
		Name:        name,
		Description: payload.Description,
		Config:      payload.Config,
		CreatedAt:   current.Preset.CreatedAt,
		UpdatedAt:   time.Now().UTC(),
	}, payload.Revision)
	if err != nil {
		writePresetError(w, err)
		return
	}
	writePresetJSON(w, http.StatusOK, presetResponse(record))
}

// Delete handles DELETE /api/v1/presets/{name}. An optional revision query
// parameter makes the delete conditional; without one it is unconditional.
func (h *PresetsController) Delete(w http.ResponseWriter, r *http.Request) {
	if h.repository == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, presetsNotConfiguredMessage)
		return
	}
	var revision uint64
	if raw := r.URL.Query().Get("revision"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid revision parameter")
			return
		}
		revision = parsed
	}
	if err := h.repository.Delete(r.Context(), chi.URLParam(r, "name"), revision); err != nil {
		writePresetError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodePresetPayload decodes one strict preset body. Unknown fields are
// rejected so config typos fail loudly instead of storing silently ignored
// settings.
func decodePresetPayload(w http.ResponseWriter, body io.Reader) (presetPayload, bool) {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var payload presetPayload
	if err := decoder.Decode(&payload); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body: "+err.Error())
		return presetPayload{}, false
	}
	return payload, true
}

func writePresetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, presets.ErrNotFound):
		dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Preset not found")
	case errors.Is(err, presets.ErrConflict):
		dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest, "Preset revision conflict")
	case errors.Is(err, presets.ErrNameImmutable):
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Preset name is immutable")
	case errors.Is(err, presets.ErrCorruptRecord):
		log.Error().Err(err).Msg("preset record is corrupt")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Preset record is invalid")
	case errors.Is(err, presets.ErrInvalidPreset):
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
	default:
		log.Error().Err(err).Msg("preset operation failed")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Preset operation failed")
	}
}

func writePresetJSON(w http.ResponseWriter, status int, payload any) {
	if err := dto.WriteJSON(w, status, payload); err != nil {
		log.Error().Err(err).Msg("failed to write preset response")
	}
}
