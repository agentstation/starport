package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/dto"
)

// Account listing bounds. They match the key listing so an operator paging
// two admin collections meets one contract.
const (
	accountListDefaultLimit = 100
	accountListMaxLimit     = 1000

	// accountKeyScanMaxPages bounds the scan that proves an account holds no
	// gateway API key before it is deleted.
	accountKeyScanMaxPages = 20
)

// KeyLister lists gateway API keys. The account surface holds this single
// method rather than the identity repository: deleting an account must be able
// to find out whether a key still names it, and nothing more.
type KeyLister interface {
	List(ctx context.Context, limit, offset int) ([]identity.Record, error)
}

// AccountsController serves the operator's account plane: the accounts that
// hold gateway API keys, the caps that bound what each account may spend, and
// the credential policy that says which provider credentials serve it.
type AccountsController struct {
	accounts account.Repository
	keys     KeyLister
}

// NewAccountsController creates the account controller. A nil repository
// degrades every route to 503 rather than to an empty account list, which
// would read as "this deployment has no accounts".
func NewAccountsController(accounts account.Repository, keys KeyLister) *AccountsController {
	return &AccountsController{accounts: accounts, keys: keys}
}

// ready reports whether account storage is configured, writing the refusal
// itself when it is not.
func (h *AccountsController) ready(w http.ResponseWriter) bool {
	if h == nil || h.accounts == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError,
			"Account management is not configured")
		return false
	}
	return true
}

// accountRequest is the writable surface of an account. Every field is a
// pointer so an update changes only what the caller named; the ID is not
// writable, because an account ID reaches a credential storage scope and a
// usage counter, and renaming it would orphan both.
type accountRequest struct {
	ID                 string                      `json:"id"`
	Name               *string                     `json:"name,omitempty"`
	Limits             *limits.Limits              `json:"limits,omitempty"`
	CredentialStrategy *account.CredentialStrategy `json:"credential_strategy,omitempty"`
	// BYOKPolicy set to {"mode":"all"} clears the stored policy, because an
	// all-providers policy and no policy mean the same thing. Access set to
	// an explicit empty list clears the stored grants the same way.
	BYOKPolicy *account.BYOKPolicy       `json:"byok_policy,omitempty"`
	Access     *[]account.ProviderAccess `json:"access,omitempty"`
	Metadata   map[string]any            `json:"metadata,omitempty"`
	Active     *bool                     `json:"active,omitempty"`
}

// List handles GET /api/v1/admin/accounts.
func (h *AccountsController) List(w http.ResponseWriter, r *http.Request) {
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
	records, err := h.accounts.List(r.Context(), limit+1, offset)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list accounts")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list accounts")
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	accounts := make([]account.Account, 0, len(records))
	for _, record := range records {
		accounts = append(accounts, effectiveAccount(record.Account))
	}

	response := map[string]any{
		"accounts":         accounts,
		responseCountField: len(accounts),
		"pagination": map[string]any{
			fieldLimit: limit,
			"offset":   offset,
			"has_more": hasMore,
		},
	}
	writeAccountJSON(w, http.StatusOK, response)
}

// Create handles POST /api/v1/admin/accounts.
func (h *AccountsController) Create(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	var request accountRequest
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
	candidate := account.Account{ID: request.ID, Name: request.ID, Active: true}
	applyAccountRequest(&candidate, request)

	if err := candidate.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	record, err := h.accounts.Create(r.Context(), candidate)
	if err != nil {
		if errors.Is(err, account.ErrConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"An account with this id already exists")
			return
		}
		log.Error().Err(err).Str(fieldAccountID, candidate.ID).Msg("Failed to create account")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to create account")
		return
	}

	writeAccountJSON(w, http.StatusCreated, effectiveAccount(record.Account))
}

// Get handles GET /api/v1/admin/accounts/{account_id}.
func (h *AccountsController) Get(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}
	writeAccountJSON(w, http.StatusOK, effectiveAccount(record.Account))
}

// Update handles PUT /api/v1/admin/accounts/{account_id}. It reads, applies the
// named fields, and writes at the revision it read, so a concurrent operator
// edit is reported as a conflict rather than silently overwritten.
func (h *AccountsController) Update(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}

	var request accountRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	edited := record.Account
	applyAccountRequest(&edited, request)
	if err := edited.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	updated, err := h.accounts.Update(r.Context(), edited, record.Revision)
	if err != nil {
		if errors.Is(err, account.ErrConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"Account changed during update")
			return
		}
		log.Error().Err(err).Str(fieldAccountID, edited.ID).Msg("Failed to update account")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to update account")
		return
	}

	writeAccountJSON(w, http.StatusOK, effectiveAccount(updated.Account))
}

// Delete handles DELETE /api/v1/admin/accounts/{account_id}. It refuses the
// canonical account, and it refuses an account that still holds a gateway API
// key: such a key would keep authenticating with no account behind it, and it
// would then run under the default credential policy rather than the one the
// operator just deleted.
func (h *AccountsController) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	accountID := chi.URLParam(r, fieldAccountID)
	if accountID == account.DefaultID {
		dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
			"The default account cannot be deleted")
		return
	}

	record, ok := h.read(w, r)
	if !ok {
		return
	}

	if message, blocked := h.accountStillHoldsKeys(r.Context(), accountID); blocked {
		dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest, message)
		return
	}

	if err := h.accounts.Delete(r.Context(), accountID, record.Revision); err != nil {
		switch {
		case errors.Is(err, account.ErrNotFound):
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Account not found")
		case errors.Is(err, account.ErrDefaultImmutable):
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"The default account cannot be deleted")
		case errors.Is(err, account.ErrConflict):
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"Account changed during delete")
		default:
			log.Error().Err(err).Str(fieldAccountID, accountID).Msg("Failed to delete account")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to delete account")
		}
		return
	}

	writeAccountJSON(w, http.StatusOK, map[string]any{
		responseMessageField: "Account deleted successfully",
		fieldAccountID:       accountID,
	})
}

// accountStillHoldsKeys reports the refusal message when a gateway API key
// still names this account. An unreadable or unbounded key listing also
// refuses: an unproven answer here would orphan a working credential.
func (h *AccountsController) accountStillHoldsKeys(ctx context.Context, accountID string) (string, bool) {
	if h.keys == nil {
		return "", false
	}
	for page := range accountKeyScanMaxPages {
		records, err := h.keys.List(ctx, accountListMaxLimit, page*accountListMaxLimit)
		if err != nil {
			log.Error().Err(err).Str(fieldAccountID, accountID).
				Msg("Failed to read gateway API keys before deleting an account")
			return "Cannot confirm that this account holds no gateway API keys", true
		}
		for _, record := range records {
			if record.APIKey.EffectiveAccountID() == accountID {
				return "This account still holds gateway API keys; delete or reassign them first", true
			}
		}
		if len(records) < accountListMaxLimit {
			return "", false
		}
	}
	return "Cannot confirm that this account holds no gateway API keys", true
}

// read loads the addressed account, writing the refusal itself when it cannot.
func (h *AccountsController) read(w http.ResponseWriter, r *http.Request) (account.Record, bool) {
	accountID := chi.URLParam(r, fieldAccountID)
	if accountID == "" {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Missing account id")
		return account.Record{}, false
	}

	record, err := h.accounts.GetByID(r.Context(), accountID)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Account not found")
			return account.Record{}, false
		}
		log.Error().Err(err).Str(fieldAccountID, accountID).Msg("Failed to read account")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to read account")
		return account.Record{}, false
	}
	return record, true
}

// applyAccountRequest copies the named fields onto an account.
func applyAccountRequest(account *account.Account, request accountRequest) {
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
	if request.BYOKPolicy != nil {
		account.BYOKPolicy = request.BYOKPolicy
		if request.BYOKPolicy.Mode == "all" && len(request.BYOKPolicy.Providers) == 0 {
			account.BYOKPolicy = nil
		}
	}
	if request.Access != nil {
		account.Access = *request.Access
		if len(*request.Access) == 0 {
			account.Access = nil
		}
	}
	if request.Metadata != nil {
		account.Metadata = request.Metadata
	}
	if request.Active != nil {
		account.Active = *request.Active
	}
}

// effectiveAccount reports the policy the account actually runs under, so a
// caller never has to know that an unset strategy means the default one.
func effectiveAccount(account account.Account) account.Account {
	account.CredentialStrategy = account.EffectiveCredentialStrategy()
	return account
}

func writeAccountJSON(w http.ResponseWriter, status int, body any) {
	if err := dto.WriteJSON(w, status, body); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}
