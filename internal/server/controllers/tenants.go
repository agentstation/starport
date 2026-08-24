package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/tenant"
)

// Account listing bounds. They match the key listing so an operator paging
// two admin collections meets one contract.
const (
	tenantListDefaultLimit = 100
	tenantListMaxLimit     = 1000

	// tenantKeyScanMaxPages bounds the scan that proves an account holds no
	// gateway API key before it is deleted.
	tenantKeyScanMaxPages = 20
)

// KeyLister lists gateway API keys. The account surface holds this single
// method rather than the identity repository: deleting an account must be able
// to find out whether a key still names it, and nothing more.
type KeyLister interface {
	List(ctx context.Context, limit, offset int) ([]identity.Record, error)
}

// TenantsController serves the operator's account plane: the accounts that
// hold gateway API keys, the caps that bound what each account may spend, and
// the credential policy that says which provider credentials serve it.
type TenantsController struct {
	tenants tenant.Repository
	keys    KeyLister
}

// NewTenantsController creates the account controller. A nil repository
// degrades every route to 503 rather than to an empty account list, which
// would read as "this deployment has no accounts".
func NewTenantsController(tenants tenant.Repository, keys KeyLister) *TenantsController {
	return &TenantsController{tenants: tenants, keys: keys}
}

// ready reports whether account storage is configured, writing the refusal
// itself when it is not.
func (h *TenantsController) ready(w http.ResponseWriter) bool {
	if h == nil || h.tenants == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError,
			"Account management is not configured")
		return false
	}
	return true
}

// tenantRequest is the writable surface of an account. Every field is a
// pointer so an update changes only what the caller named; the ID is not
// writable, because an account ID reaches a credential storage scope and a
// usage counter, and renaming it would orphan both.
type tenantRequest struct {
	ID                 string                     `json:"id"`
	Name               *string                    `json:"name,omitempty"`
	Limits             *limits.Limits             `json:"limits,omitempty"`
	CredentialStrategy *tenant.CredentialStrategy `json:"credential_strategy,omitempty"`
	Metadata           map[string]any             `json:"metadata,omitempty"`
	Active             *bool                      `json:"active,omitempty"`
}

// List handles GET /api/v1/admin/tenants.
func (h *TenantsController) List(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	limit, err := positiveQueryInt(r, fieldLimit, tenantListDefaultLimit, tenantListMaxLimit)
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
	records, err := h.tenants.List(r.Context(), limit+1, offset)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list accounts")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list accounts")
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	accounts := make([]tenant.Tenant, 0, len(records))
	for _, record := range records {
		accounts = append(accounts, effectiveTenant(record.Tenant))
	}

	response := map[string]any{
		"tenants":          accounts,
		responseCountField: len(accounts),
		"pagination": map[string]any{
			fieldLimit: limit,
			"offset":   offset,
			"has_more": hasMore,
		},
	}
	writeTenantJSON(w, http.StatusOK, response)
}

// Create handles POST /api/v1/admin/tenants.
func (h *TenantsController) Create(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	var request tenantRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}
	if request.ID == "" {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "id is required")
		return
	}

	// A new account is active unless the caller says otherwise, and its name
	// falls back to its ID so a caller never has to repeat itself.
	account := tenant.Tenant{ID: request.ID, Name: request.ID, Active: true}
	applyTenantRequest(&account, request)

	if err := account.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	record, err := h.tenants.Create(r.Context(), account)
	if err != nil {
		if errors.Is(err, tenant.ErrConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"An account with this id already exists")
			return
		}
		log.Error().Err(err).Str(fieldTenantID, account.ID).Msg("Failed to create account")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to create account")
		return
	}

	writeTenantJSON(w, http.StatusCreated, effectiveTenant(record.Tenant))
}

// Get handles GET /api/v1/admin/tenants/{tenant_id}.
func (h *TenantsController) Get(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}
	writeTenantJSON(w, http.StatusOK, effectiveTenant(record.Tenant))
}

// Update handles PUT /api/v1/admin/tenants/{tenant_id}. It reads, applies the
// named fields, and writes at the revision it read, so a concurrent operator
// edit is reported as a conflict rather than silently overwritten.
func (h *TenantsController) Update(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}

	var request tenantRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	account := record.Tenant
	applyTenantRequest(&account, request)
	if err := account.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	updated, err := h.tenants.Update(r.Context(), account, record.Revision)
	if err != nil {
		if errors.Is(err, tenant.ErrConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"Account changed during update")
			return
		}
		log.Error().Err(err).Str(fieldTenantID, account.ID).Msg("Failed to update account")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to update account")
		return
	}

	writeTenantJSON(w, http.StatusOK, effectiveTenant(updated.Tenant))
}

// Delete handles DELETE /api/v1/admin/tenants/{tenant_id}. It refuses the
// canonical account, and it refuses an account that still holds a gateway API
// key: such a key would keep authenticating with no account behind it, and it
// would then run under the default credential policy rather than the one the
// operator just deleted.
func (h *TenantsController) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	tenantID := chi.URLParam(r, fieldTenantID)
	if tenantID == tenant.DefaultID {
		dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
			"The default account cannot be deleted")
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}

	if message, blocked := h.accountStillHoldsKeys(r.Context(), tenantID); blocked {
		dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest, message)
		return
	}

	if err := h.tenants.Delete(r.Context(), tenantID, record.Revision); err != nil {
		switch {
		case errors.Is(err, tenant.ErrNotFound):
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Account not found")
		case errors.Is(err, tenant.ErrDefaultImmutable):
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"The default account cannot be deleted")
		case errors.Is(err, tenant.ErrConflict):
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"Account changed during delete")
		default:
			log.Error().Err(err).Str(fieldTenantID, tenantID).Msg("Failed to delete account")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to delete account")
		}
		return
	}

	writeTenantJSON(w, http.StatusOK, map[string]any{
		responseMessageField: "Account deleted successfully",
		fieldTenantID:        tenantID,
	})
}

// accountStillHoldsKeys reports the refusal message when a gateway API key
// still names this account. An unreadable or unbounded key listing also
// refuses: an unproven answer here would orphan a working credential.
func (h *TenantsController) accountStillHoldsKeys(ctx context.Context, tenantID string) (string, bool) {
	if h.keys == nil {
		return "", false
	}
	for page := range tenantKeyScanMaxPages {
		records, err := h.keys.List(ctx, tenantListMaxLimit, page*tenantListMaxLimit)
		if err != nil {
			log.Error().Err(err).Str(fieldTenantID, tenantID).
				Msg("Failed to read gateway API keys before deleting an account")
			return "Cannot confirm that this account holds no gateway API keys", true
		}
		for _, record := range records {
			if record.APIKey.EffectiveTenantID() == tenantID {
				return "This account still holds gateway API keys; delete or reassign them first", true
			}
		}
		if len(records) < tenantListMaxLimit {
			return "", false
		}
	}
	return "Cannot confirm that this account holds no gateway API keys", true
}

// read loads the addressed account, writing the refusal itself when it cannot.
func (h *TenantsController) read(w http.ResponseWriter, r *http.Request) (tenant.Record, bool) {
	tenantID := chi.URLParam(r, fieldTenantID)
	if tenantID == "" {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Missing account id")
		return tenant.Record{}, false
	}

	record, err := h.tenants.GetByID(r.Context(), tenantID)
	if err != nil {
		if errors.Is(err, tenant.ErrNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Account not found")
			return tenant.Record{}, false
		}
		log.Error().Err(err).Str(fieldTenantID, tenantID).Msg("Failed to read account")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to read account")
		return tenant.Record{}, false
	}
	return record, true
}

// applyTenantRequest copies the named fields onto an account.
func applyTenantRequest(account *tenant.Tenant, request tenantRequest) {
	if request.Name != nil {
		account.Name = *request.Name
	}
	if request.Limits != nil {
		// An explicit empty object clears every cap.
		account.Limits = request.Limits
		if request.Limits.IsZero() {
			account.Limits = nil
		}
	}
	if request.CredentialStrategy != nil {
		account.CredentialStrategy = *request.CredentialStrategy
	}
	if request.Metadata != nil {
		account.Metadata = request.Metadata
	}
	if request.Active != nil {
		account.Active = *request.Active
	}
}

// effectiveTenant reports the policy the account actually runs under, so a
// caller never has to know that an unset strategy means the default one.
func effectiveTenant(account tenant.Tenant) tenant.Tenant {
	account.CredentialStrategy = account.EffectiveCredentialStrategy()
	return account
}

func writeTenantJSON(w http.ResponseWriter, status int, body any) {
	if err := dto.WriteJSON(w, status, body); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}
