package controllers

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/dto"
)

const fieldTemplateID = "template_id"

// AccountTemplatesController serves the operator's account templates: the
// named creation defaults an account can be stamped from. It manages the
// templates alone; the stamping itself happens where accounts are created.
type AccountTemplatesController struct {
	templates account.TemplateRepository
	audit     AuditRecorder
}

// NewAccountTemplatesController creates the template controller. A nil
// repository degrades every route to 503 rather than to an empty template
// list, which would read as "this deployment has no templates".
func NewAccountTemplatesController(templates account.TemplateRepository) *AccountTemplatesController {
	return &AccountTemplatesController{templates: templates}
}

// ready reports whether template storage is configured, writing the refusal
// itself when it is not.
func (h *AccountTemplatesController) ready(w http.ResponseWriter) bool {
	if h == nil || h.templates == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError,
			"Account template management is not configured")
		return false
	}
	return true
}

// templateRequest is the writable surface of a template. Every field is a
// pointer so an update changes only what the caller named, and the clearing
// sentinels match the account surface: {"mode":"all"} clears the BYOK
// policy, an explicit empty access list clears the grants, and an all-zero
// limits object clears the cap.
type templateRequest struct {
	ID                 string                      `json:"id"`
	Name               *string                     `json:"name,omitempty"`
	Limits             *limits.Limits              `json:"limits,omitempty"`
	CredentialStrategy *account.CredentialStrategy `json:"credential_strategy,omitempty"`
	BYOKPolicy         *account.BYOKPolicy         `json:"byok_policy,omitempty"`
	Access             *[]account.ProviderAccess   `json:"access,omitempty"`
}

// List handles GET /api/v1/admin/account-templates.
func (h *AccountTemplatesController) List(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	limit, err := positiveQueryInt(r, fieldLimit, accountListDefaultLimit, accountListMaxLimit)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}
	offset, err := positiveQueryInt(r, "offset", 0, math.MaxInt)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	// One extra record proves or disproves a following page.
	records, err := h.templates.List(r.Context(), limit+1, offset)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list account templates")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
			"Failed to list account templates")
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	templates := make([]account.Template, 0, len(records))
	for _, record := range records {
		templates = append(templates, record.Template)
	}

	response := map[string]any{
		"templates":             templates,
		responseCountField:      len(templates),
		responsePaginationField: paginationEnvelope(limit, offset, hasMore),
	}
	writeAccountJSON(w, http.StatusOK, response)
}

// Create handles POST /api/v1/admin/account-templates.
func (h *AccountTemplatesController) Create(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	var request templateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}
	if request.ID == "" {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "id is required")
		return
	}

	// A template's name falls back to its ID the way an account's does.
	candidate := account.Template{ID: request.ID, Name: request.ID}
	applyTemplateRequest(&candidate, request)

	if err := candidate.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	record, err := h.templates.Create(r.Context(), candidate)
	writeAudit(r.Context(), h.audit, "template.create", candidate.ID, err)
	if err != nil {
		if errors.Is(err, account.ErrTemplateConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"A template with this id already exists")
			return
		}
		log.Error().Err(err).Str(fieldTemplateID, candidate.ID).Msg("Failed to create account template")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
			"Failed to create account template")
		return
	}

	writeAccountJSON(w, http.StatusCreated, record.Template)
}

// Get handles GET /api/v1/admin/account-templates/{template_id}.
func (h *AccountTemplatesController) Get(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}
	writeAccountJSON(w, http.StatusOK, record.Template)
}

// Update handles PUT /api/v1/admin/account-templates/{template_id}. It
// reads, applies the named fields, and writes at the revision it read, so a
// concurrent operator edit is reported as a conflict rather than silently
// overwritten.
func (h *AccountTemplatesController) Update(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}

	var request templateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	edited := record.Template
	applyTemplateRequest(&edited, request)
	if err := edited.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	updated, err := h.templates.Update(r.Context(), edited, record.Revision)
	writeAudit(r.Context(), h.audit, "template.update", edited.ID, err)
	if err != nil {
		if errors.Is(err, account.ErrTemplateConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"Template changed during update")
			return
		}
		log.Error().Err(err).Str(fieldTemplateID, edited.ID).Msg("Failed to update account template")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
			"Failed to update account template")
		return
	}

	writeAccountJSON(w, http.StatusOK, updated.Template)
}

// Delete handles DELETE /api/v1/admin/account-templates/{template_id}. An
// account stamped from the template keeps its copied defaults, so deleting
// a template strands nothing.
func (h *AccountTemplatesController) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}

	err := h.templates.Delete(r.Context(), record.Template.ID, record.Revision)
	writeAudit(r.Context(), h.audit, "template.delete", record.Template.ID, err)
	if err != nil {
		switch {
		case errors.Is(err, account.ErrTemplateNotFound):
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Template not found")
		case errors.Is(err, account.ErrTemplateConflict):
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"Template changed during delete")
		default:
			log.Error().Err(err).Str(fieldTemplateID, record.Template.ID).
				Msg("Failed to delete account template")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
				"Failed to delete account template")
		}
		return
	}

	writeAccountJSON(w, http.StatusOK, map[string]any{
		responseMessageField: "Template deleted successfully",
		fieldTemplateID:      record.Template.ID,
	})
}

// read loads the addressed template, writing the refusal itself when it
// cannot.
func (h *AccountTemplatesController) read(w http.ResponseWriter, r *http.Request) (account.TemplateRecord, bool) {
	templateID := chi.URLParam(r, fieldTemplateID)
	if templateID == "" {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Missing template id")
		return account.TemplateRecord{}, false
	}

	record, err := h.templates.GetByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, account.ErrTemplateNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Template not found")
			return account.TemplateRecord{}, false
		}
		log.Error().Err(err).Str(fieldTemplateID, templateID).Msg("Failed to read account template")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
			"Failed to read account template")
		return account.TemplateRecord{}, false
	}
	return record, true
}

// applyTemplateRequest copies the named fields onto a template, honoring
// the same clearing sentinels the account surface honors.
func applyTemplateRequest(template *account.Template, request templateRequest) {
	if request.Name != nil {
		template.Name = *request.Name
	}
	if request.Limits != nil {
		// An explicit empty object clears every cap.
		template.Limits = request.Limits
		if request.Limits.IsZero() {
			template.Limits = nil
		}
	}
	if request.CredentialStrategy != nil {
		template.CredentialStrategy = *request.CredentialStrategy
	}
	if request.BYOKPolicy != nil {
		template.BYOKPolicy = request.BYOKPolicy
		if request.BYOKPolicy.Mode == account.BYOKAll && len(request.BYOKPolicy.Providers) == 0 {
			template.BYOKPolicy = nil
		}
	}
	if request.Access != nil {
		template.Access = *request.Access
		if len(*request.Access) == 0 {
			template.Access = nil
		}
	}
}
