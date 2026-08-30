package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/server/dto"
)

// accountPolicyReader loads the account a BYOK route addresses, so a write
// can honor the operator's BYOK policy. It is the read half of
// account.Repository.
type accountPolicyReader interface {
	GetByID(ctx context.Context, id string) (account.Record, error)
}

// ProviderCredentialsController serves both stored provider-credential planes.
//
// A shared credential belongs to the operator and serves the deployment's
// accounts. A BYOK credential belongs to one account and serves only that
// account. The plane is always named by the route rather than derived from the
// gateway API key that carried the request.
type ProviderCredentialsController struct {
	providerKeys keyring.ProviderKeys
	accounts     accountPolicyReader
	audit        AuditRecorder
}

// NewProviderCredentialsController creates the credential controller. A nil
// accounts reader disables BYOK-policy enforcement, which only a test uses.
func NewProviderCredentialsController(
	providerKeys keyring.ProviderKeys,
	accounts accountPolicyReader,
) *ProviderCredentialsController {
	return &ProviderCredentialsController{providerKeys: providerKeys, accounts: accounts}
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
// The shared plane is a list per provider, and the routes address it as one:
// the collection lists and creates, and each credential is reachable by the
// id a create reported. Secrets never return on any of them.

// fieldCredentialID is the item route parameter naming one shared credential.
const fieldCredentialID = "credential_id" // #nosec G101 -- URL route parameter name, not credential material.

// sharedCredentialRequest is the body a shared create accepts. Credentials
// and config are the catalog-declared fields every credential write carries;
// label, access, and grants are the sharing facts. An absent access is open:
// a credential the operator applies without saying otherwise serves every
// account.
type sharedCredentialRequest struct {
	Credentials map[string]any `json:"credentials"`
	Config      map[string]any `json:"config,omitempty"`
	Label       string         `json:"label,omitempty"`
	Access      string         `json:"access,omitempty"`
	Grants      []string       `json:"grants,omitempty"`
}

// sharedCredentialUpdateRequest is the body a shared item PUT accepts. Every
// field is optional, so an operator can rotate a value, rename a credential,
// or restate its grants without restating the rest. Grants replace the whole
// list because a grant change is a statement of who may spend, not a diff.
type sharedCredentialUpdateRequest struct {
	Credentials map[string]any `json:"credentials,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
	Label       *string        `json:"label,omitempty"`
	Access      *string        `json:"access,omitempty"`
	Grants      *[]string      `json:"grants,omitempty"`
}

// SharedList handles GET /api/v1/providers/{provider}/credentials. An empty
// plane is an empty collection: "no credential is stored" is an answer about
// the list, not a lookup failure.
func (h *ProviderCredentialsController) SharedList(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)
	shared, err := h.providerKeys.GetSharedCredentials(r.Context(), provider)
	if err != nil {
		h.writeLookupError(w, err, provider, "list")
		return
	}
	summaries := make([]map[string]any, len(shared))
	for i := range shared {
		summaries[i] = sharedCredentialSummary(provider, &shared[i])
	}
	writeCredentialResponse(w, http.StatusOK, map[string]any{
		"credentials":      summaries,
		responseCountField: len(summaries),
	})
}

// SharedCreate handles POST /api/v1/providers/{provider}/credentials. The
// response names the id that addresses the new credential from now on.
func (h *ProviderCredentialsController) SharedCreate(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)

	var request sharedCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}
	fields, ok := credentialFields(w, request.Credentials, true)
	if !ok {
		return
	}

	credential, err := h.providerKeys.AddSharedCredential(r.Context(), provider, fields, request.Config,
		keyring.SharedCredentialParams{
			Label:  request.Label,
			Access: credentials.Access(request.Access),
			Grants: request.Grants,
		})
	subject := provider
	if err == nil {
		subject = provider + "/" + credential.ID
	}
	writeAudit(r.Context(), h.audit, "credential.create", subject, err)
	if err != nil {
		h.writeWriteError(w, err, provider, "store")
		return
	}
	writeCredentialResponse(w, http.StatusCreated, sharedCredentialSummary(provider, credential))
}

// SharedGet handles GET /api/v1/providers/{provider}/credentials/{credential_id}.
func (h *ProviderCredentialsController) SharedGet(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)
	credential, ok := h.sharedCredentialByID(w, r, provider)
	if !ok {
		return
	}
	writeCredentialResponse(w, http.StatusOK, sharedCredentialSummary(provider, credential))
}

// SharedUpdate handles PUT /api/v1/providers/{provider}/credentials/{credential_id}.
func (h *ProviderCredentialsController) SharedUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)
	credentialID := chi.URLParam(r, fieldCredentialID)

	var request sharedCredentialUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}
	update := keyring.SharedCredentialUpdate{
		Config: request.Config,
		Label:  request.Label,
		Grants: request.Grants,
	}
	if len(request.Credentials) > 0 {
		fields, ok := credentialFields(w, request.Credentials, false)
		if !ok {
			return
		}
		update.Key = fields
	}
	if request.Access != nil {
		access := credentials.Access(*request.Access)
		update.Access = &access
	}

	credential, err := h.providerKeys.UpdateSharedCredential(r.Context(), provider, credentialID, update)
	writeAudit(r.Context(), h.audit, "credential.update", provider+"/"+credentialID, err)
	if err != nil {
		if errors.Is(err, keyring.ErrKeyNotFound) {
			h.writeLookupError(w, err, provider, "update")
			return
		}
		h.writeWriteError(w, err, provider, "update")
		return
	}
	writeCredentialResponse(w, http.StatusOK, sharedCredentialSummary(provider, credential))
}

// SharedDelete handles DELETE /api/v1/providers/{provider}/credentials/{credential_id}.
func (h *ProviderCredentialsController) SharedDelete(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)
	credentialID := chi.URLParam(r, fieldCredentialID)
	err := h.providerKeys.DeleteSharedCredential(r.Context(), provider, credentialID)
	writeAudit(r.Context(), h.audit, "credential.delete", provider+"/"+credentialID, err)
	if err != nil {
		h.writeLookupError(w, err, provider, "delete")
		return
	}
	writeCredentialResponse(w, http.StatusOK, map[string]any{
		responseMessageField: "Provider credential removed",
		providerField:        provider,
	})
}

// SharedValidate handles POST /api/v1/providers/{provider}/credentials/{credential_id}/validate.
func (h *ProviderCredentialsController) SharedValidate(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	provider := chi.URLParam(r, providerField)
	if _, ok := h.sharedCredentialByID(w, r, provider); !ok {
		return
	}
	writeCredentialResponse(w, http.StatusOK, map[string]any{
		"valid":              true,
		responseMessageField: "A credential is stored for this provider",
		"note":               "Stored credentials are encrypted; this does not test them against the provider",
	})
}

// sharedCredentialByID reads the provider's shared list and addresses the
// entry the route names. An unknown id is the same 404 a missing record is.
func (h *ProviderCredentialsController) sharedCredentialByID(
	w http.ResponseWriter, r *http.Request, provider string,
) (*credentials.SharedCredential, bool) {
	credentialID := chi.URLParam(r, fieldCredentialID)
	shared, err := h.providerKeys.GetSharedCredentials(r.Context(), provider)
	if err != nil {
		h.writeLookupError(w, err, provider, "read")
		return nil, false
	}
	for i := range shared {
		if shared[i].ID == credentialID {
			return &shared[i], true
		}
	}
	h.writeLookupError(w, keyring.ErrKeyNotFound, provider, "read")
	return nil, false
}

// sharedCredentialSummary is the only shape the shared plane returns for one
// credential. It names the credential and its sharing facts and never carries
// the encrypted value.
func sharedCredentialSummary(provider string, credential *credentials.SharedCredential) map[string]any {
	grants := credential.Grants
	if grants == nil {
		grants = []string{}
	}
	summary := map[string]any{
		"id":              credential.ID,
		providerField:     provider,
		"label":           credential.Label,
		"access":          string(credential.Access),
		"grants":          grants,
		"has_credentials": true,
		"config":          credential.Config,
		fieldCreatedAt:    credential.CreatedAt.UTC().Format(time.RFC3339),
		"usage_count":     credential.UsageCount,
	}
	if credential.LastUsed != nil {
		summary["last_used"] = credential.LastUsed.UTC().Format(time.RFC3339)
	}
	return summary
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

// BYOKPut handles PUT /api/v1/accounts/{account_id}/byok/{provider}. The
// write is where the operator's BYOK policy speaks: an account outside the
// policy never stores the credential, so nothing later has to unwind one.
func (h *ProviderCredentialsController) BYOKPut(w http.ResponseWriter, r *http.Request) {
	if !h.byokAllowed(w, r) {
		return
	}
	h.put(w, r, byokScope(r))
}

// byokAllowed reports whether the operator's BYOK policy lets the addressed
// account store its own credential for the routed provider, and writes the
// refusal when it does not. A missing account or reader falls open: the
// account middleware already decided the route is reachable, and a policy
// only exists on an account an operator stored one on.
func (h *ProviderCredentialsController) byokAllowed(w http.ResponseWriter, r *http.Request) bool {
	if h.accounts == nil {
		return true
	}
	accountID := chi.URLParam(r, fieldAccountID)
	record, err := h.accounts.GetByID(r.Context(), accountID)
	if err != nil {
		if !errors.Is(err, account.ErrNotFound) {
			log.Error().Err(err).Str("account", accountID).
				Msg("Failed to read account for BYOK policy check")
		}
		return true
	}
	provider := chi.URLParam(r, providerField)
	if record.Account.AllowsBYOK(provider) {
		return true
	}
	dto.WriteError(w, http.StatusForbidden, dto.ErrorTypePermissionError,
		"This account may not bring its own credential for provider "+provider)
	return false
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

	subject := chi.URLParam(r, fieldAccountID) + "/" + provider
	_, err := h.providerKeys.GetKey(ctx, scope, provider)
	switch {
	case err == nil:
		_, updateErr := h.providerKeys.UpdateKey(ctx, scope, provider, fields, config, nil, nil)
		writeAudit(ctx, h.audit, "byok.put", subject, updateErr)
		if updateErr != nil {
			h.writeWriteError(w, updateErr, provider, "update")
			return
		}
	case errors.Is(err, keyring.ErrKeyNotFound):
		_, addErr := h.providerKeys.AddKey(ctx, scope, provider, fields, config, false, 0)
		writeAudit(ctx, h.audit, "byok.put", subject, addErr)
		if addErr != nil {
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

	err := h.providerKeys.DeleteKey(ctx, scope, provider)
	writeAudit(ctx, h.audit, "byok.delete", chi.URLParam(r, fieldAccountID)+"/"+provider, err)
	if err != nil {
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
	if errors.Is(err, credentials.ErrInvalidAccess) {
		dto.WriteValidationError(w, "access", err.Error())
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

	fields, ok := credentialFields(w, request.Credentials, true)
	if !ok {
		return nil, nil, false
	}
	return fields, request.Config, true
}

// credentialFields checks that every credential value is a string: a
// credential field is a secret or a parameter, and neither is a nested
// object. When required, an empty map is the caller's mistake too.
func credentialFields(w http.ResponseWriter, values map[string]any, required bool) (map[string]string, bool) {
	if len(values) == 0 {
		if required {
			dto.WriteValidationError(w, "credentials", "Credentials are required")
			return nil, false
		}
		return nil, true
	}

	fields := make(map[string]string, len(values))
	for name, value := range values {
		text, ok := value.(string)
		if !ok {
			dto.WriteValidationError(w, "credentials."+name, "Credential values must be strings")
			return nil, false
		}
		fields[name] = text
	}
	return fields, true
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
