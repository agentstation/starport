package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/server/dto"
)

// ProviderCredentialsController serves both stored provider-credential planes.
//
// A shared credential belongs to the operator and serves the deployment's
// accounts. A BYOK credential belongs to one account and serves only that
// account. The plane is always named by the route rather than derived from the
// gateway API key that carried the request.
type ProviderCredentialsController struct {
	providerKeys keyring.ProviderKeys
}

// NewProviderCredentialsController creates the credential controller.
func NewProviderCredentialsController(providerKeys keyring.ProviderKeys) *ProviderCredentialsController {
	return &ProviderCredentialsController{providerKeys: providerKeys}
}

// credentialRequest is the body both surfaces accept. A credential is a map of
// catalog-declared fields, so the gateway never learns provider-specific field
// names and a new provider needs no code here.
type credentialRequest struct {
	Credentials map[string]any `json:"credentials"`
	Config      map[string]any `json:"config,omitempty"`
}

// --- the operator's shared plane ---
//
// These routes address the provider's shared credential list through its first
// entry, so an operator with one credential per provider keeps the surface
// they had. The by-id surface that manages the whole list is the next task's.

// SharedGet handles GET /api/v1/providers/{provider}/credentials.
func (h *ProviderCredentialsController) SharedGet(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)
	credential, ok := h.firstSharedCredential(w, r, provider)
	if !ok {
		return
	}
	writeCredentialResponse(w, http.StatusOK, credentialSummary(provider, credential.Config,
		true, credential.CreatedAt, credential.LastUsed, credential.UsageCount))
}

// SharedPut handles PUT /api/v1/providers/{provider}/credentials. PUT is an
// upsert: an operator rotating a deployment credential should not have to know
// whether one is already applied. A new credential is open to every account,
// which is the sharing default.
func (h *ProviderCredentialsController) SharedPut(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx := r.Context()
	provider := chi.URLParam(r, providerField)
	fields, config, ok := decodeCredential(w, r)
	if !ok {
		return
	}

	existing, err := h.providerKeys.GetSharedCredentials(ctx, provider)
	if err != nil {
		h.writeLookupError(w, err, provider, "read")
		return
	}
	if len(existing) == 0 {
		if _, addErr := h.providerKeys.AddSharedCredential(ctx, provider, fields, config, keyring.SharedCredentialParams{}); addErr != nil {
			h.writeWriteError(w, addErr, provider, "store")
			return
		}
	} else {
		update := keyring.SharedCredentialUpdate{Key: fields, Config: config}
		if _, updateErr := h.providerKeys.UpdateSharedCredential(ctx, provider, existing[0].ID, update); updateErr != nil {
			h.writeWriteError(w, updateErr, provider, "update")
			return
		}
	}

	writeCredentialResponse(w, http.StatusOK, map[string]any{
		responseMessageField: "Provider credential applied",
		providerField:        provider,
	})
}

// SharedDelete handles DELETE /api/v1/providers/{provider}/credentials.
func (h *ProviderCredentialsController) SharedDelete(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)
	credential, ok := h.firstSharedCredential(w, r, provider)
	if !ok {
		return
	}
	if err := h.providerKeys.DeleteSharedCredential(r.Context(), provider, credential.ID); err != nil {
		h.writeLookupError(w, err, provider, "delete")
		return
	}
	writeCredentialResponse(w, http.StatusOK, map[string]any{
		responseMessageField: "Provider credential removed",
		providerField:        provider,
	})
}

// SharedValidate handles POST /api/v1/providers/{provider}/credentials/validate.
func (h *ProviderCredentialsController) SharedValidate(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)
	if _, ok := h.firstSharedCredential(w, r, provider); !ok {
		return
	}
	writeCredentialResponse(w, http.StatusOK, map[string]any{
		"valid":              true,
		responseMessageField: "A credential is stored for this provider",
		"note":               "Stored credentials are encrypted; this does not test them against the provider",
	})
}

// firstSharedCredential reads the provider's shared list and addresses its
// first entry. An empty list is the same 404 an absent record was.
func (h *ProviderCredentialsController) firstSharedCredential(
	w http.ResponseWriter, r *http.Request, provider string,
) (*credentials.SharedCredential, bool) {
	shared, err := h.providerKeys.GetSharedCredentials(r.Context(), provider)
	if err != nil {
		h.writeLookupError(w, err, provider, "read")
		return nil, false
	}
	if len(shared) == 0 {
		h.writeLookupError(w, keyring.ErrKeyNotFound, provider, "read")
		return nil, false
	}
	return &shared[0], true
}

// --- the account's own plane ---

// BYOKList handles GET /api/v1/accounts/{account_id}/byok.
func (h *ProviderCredentialsController) BYOKList(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, byokScope(r))
}

// BYOKGet handles GET /api/v1/accounts/{account_id}/byok/{provider}.
func (h *ProviderCredentialsController) BYOKGet(w http.ResponseWriter, r *http.Request) {
	h.get(w, r, byokScope(r))
}

// BYOKPut handles PUT /api/v1/accounts/{account_id}/byok/{provider}.
func (h *ProviderCredentialsController) BYOKPut(w http.ResponseWriter, r *http.Request) {
	h.put(w, r, byokScope(r))
}

// BYOKDelete handles DELETE /api/v1/accounts/{account_id}/byok/{provider}.
func (h *ProviderCredentialsController) BYOKDelete(w http.ResponseWriter, r *http.Request) {
	h.remove(w, r, byokScope(r))
}

// BYOKValidate handles POST /api/v1/accounts/{account_id}/byok/{provider}/validate.
func (h *ProviderCredentialsController) BYOKValidate(w http.ResponseWriter, r *http.Request) {
	h.validate(w, r, byokScope(r))
}

// byokScope names the account the route addresses. RequireAccountAccess has
// already decided the caller may reach it.
func byokScope(r *http.Request) string {
	return keyring.AccountScope(chi.URLParam(r, fieldAccountID))
}

// --- the shared implementations ---

func (h *ProviderCredentialsController) list(w http.ResponseWriter, r *http.Request, scope string) {
	if !h.ready(w) {
		return
	}

	ctx := r.Context()
	keys, err := h.providerKeys.ListKeys(ctx, scope)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list provider credentials")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list provider credentials")
		return
	}

	summaries := make([]map[string]any, len(keys))
	for i, key := range keys {
		summaries[i] = credentialSummary(key.Provider, key.Config, key.EncryptedCredential != "",
			key.CreatedAt, key.LastUsed, key.UsageCount)
	}

	response := map[string]any{
		"credentials":      summaries,
		responseCountField: len(summaries),
	}
	writeCredentialResponse(w, http.StatusOK, response)
}

func (h *ProviderCredentialsController) get(w http.ResponseWriter, r *http.Request, scope string) {
	if !h.ready(w) {
		return
	}

	ctx := r.Context()
	provider := chi.URLParam(r, providerField)

	key, err := h.providerKeys.GetKey(ctx, scope, provider)
	if err != nil {
		h.writeLookupError(w, err, provider, "read")
		return
	}

	writeCredentialResponse(w, http.StatusOK, credentialSummary(key.Provider, key.Config,
		key.EncryptedCredential != "", key.CreatedAt, key.LastUsed, key.UsageCount))
}

// put upserts one credential. Both surfaces treat PUT as create-or-replace,
// because the caller is stating what the credential should be, not asking
// whether one already exists.
func (h *ProviderCredentialsController) put(w http.ResponseWriter, r *http.Request, scope string) {
	if !h.ready(w) {
		return
	}

	ctx := r.Context()
	provider := chi.URLParam(r, providerField)

	fields, config, ok := decodeCredential(w, r)
	if !ok {
		return
	}

	_, err := h.providerKeys.GetKey(ctx, scope, provider)
	switch {
	case err == nil:
		if _, updateErr := h.providerKeys.UpdateKey(ctx, scope, provider, fields, config, nil, nil); updateErr != nil {
			h.writeWriteError(w, updateErr, provider, "update")
			return
		}
	case errors.Is(err, keyring.ErrKeyNotFound):
		if _, addErr := h.providerKeys.AddKey(ctx, scope, provider, fields, config, false, 0); addErr != nil {
			h.writeWriteError(w, addErr, provider, "store")
			return
		}
	default:
		h.writeLookupError(w, err, provider, "read")
		return
	}

	writeCredentialResponse(w, http.StatusOK, map[string]any{
		responseMessageField: "Provider credential applied",
		providerField:        provider,
	})
}

func (h *ProviderCredentialsController) remove(w http.ResponseWriter, r *http.Request, scope string) {
	if !h.ready(w) {
		return
	}

	ctx := r.Context()
	provider := chi.URLParam(r, providerField)

	if err := h.providerKeys.DeleteKey(ctx, scope, provider); err != nil {
		h.writeLookupError(w, err, provider, "delete")
		return
	}

	writeCredentialResponse(w, http.StatusOK, map[string]any{
		responseMessageField: "Provider credential removed",
		providerField:        provider,
	})
}

// validate reports whether a stored credential exists and satisfies the
// provider's catalog-declared credential schema. It never decrypts, so it
// cannot report whether the provider would accept the value.
func (h *ProviderCredentialsController) validate(w http.ResponseWriter, r *http.Request, scope string) {
	if !h.ready(w) {
		return
	}

	ctx := r.Context()
	provider := chi.URLParam(r, providerField)

	if _, err := h.providerKeys.GetKey(ctx, scope, provider); err != nil {
		h.writeLookupError(w, err, provider, "read")
		return
	}

	writeCredentialResponse(w, http.StatusOK, map[string]any{
		"valid":              true,
		responseMessageField: "A credential is stored for this provider",
		"note":               "Stored credentials are encrypted; this does not test them against the provider",
	})
}

func (h *ProviderCredentialsController) ready(w http.ResponseWriter) bool {
	if h.providerKeys != nil {
		return true
	}

	dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError,
		"Provider credential management is not configured")
	return false
}

// writeLookupError maps a read or delete failure. A missing credential is a
// 404 whichever plane was addressed.
func (h *ProviderCredentialsController) writeLookupError(w http.ResponseWriter, err error, provider, action string) {
	if errors.Is(err, keyring.ErrKeyNotFound) {
		dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Provider credential not found")
		return
	}
	log.Error().Err(err).Str(providerField, provider).Msgf("Failed to %s provider credential", action)
	dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
		"Failed to "+action+" provider credential")
}

// writeWriteError maps a store failure. A value that does not satisfy the
// provider's catalog-declared credential schema is the caller's mistake, so it
// is a 400 and not a 500.
func (h *ProviderCredentialsController) writeWriteError(w http.ResponseWriter, err error, provider, action string) {
	var validation *keyring.ValidationError
	if errors.As(err, &validation) {
		dto.WriteValidationError(w, "credentials", validation.Error())
		return
	}
	log.Error().Err(err).Str(providerField, provider).Msgf("Failed to %s provider credential", action)
	dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
		"Failed to "+action+" provider credential")
}

// decodeCredential reads the request body into catalog-declared fields. Every
// value must be a string: a credential field is a secret or a parameter, and
// neither is a nested object.
func decodeCredential(w http.ResponseWriter, r *http.Request) (map[string]string, map[string]any, bool) {
	var request credentialRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return nil, nil, false
	}

	if len(request.Credentials) == 0 {
		dto.WriteValidationError(w, "credentials", "Credentials are required")
		return nil, nil, false
	}

	fields := make(map[string]string, len(request.Credentials))
	for name, value := range request.Credentials {
		text, ok := value.(string)
		if !ok {
			dto.WriteValidationError(w, "credentials."+name, "Credential values must be strings")
			return nil, nil, false
		}
		fields[name] = text
	}

	return fields, request.Config, true
}

// credentialSummary is the only shape either surface returns for a stored
// credential. It reports that a credential exists and never what it is.
func credentialSummary(
	provider string,
	config map[string]any,
	hasCredentials bool,
	createdAt time.Time,
	lastUsed *time.Time,
	usageCount int64,
) map[string]any {
	summary := map[string]any{
		providerField:     provider,
		"has_credentials": hasCredentials,
		"config":          config,
		fieldCreatedAt:    createdAt.UTC().Format(time.RFC3339),
		"usage_count":     usageCount,
	}
	if lastUsed != nil {
		summary["last_used"] = lastUsed.UTC().Format(time.RFC3339)
	}
	return summary
}

func writeCredentialResponse(w http.ResponseWriter, status int, body map[string]any) {
	if err := dto.WriteJSON(w, status, body); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}
