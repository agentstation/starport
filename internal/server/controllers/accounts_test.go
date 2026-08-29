package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/storage"
)

// newAccountsTestController wires the account plane over real storage. The
// guards under test are about what the repositories actually hold, so a fake
// repository would prove nothing here.
func newAccountsTestController(t *testing.T) (*AccountsController, account.Repository, identity.Repository) {
	t.Helper()
	accounts, err := account.Open(storage.NewMockStore())
	require.NoError(t, err)
	_, err = accounts.EnsureDefault(context.Background())
	require.NoError(t, err)

	keys, err := identity.Open(storage.NewMockStore())
	require.NoError(t, err)

	return NewAccountsController(accounts, keys), accounts, keys
}

func accountsTestRouter(controller *AccountsController) chi.Router {
	router := chi.NewRouter()
	router.Get("/accounts", controller.List)
	router.Post("/accounts", controller.Create)
	router.Get("/accounts/{account_id}", controller.Get)
	router.Put("/accounts/{account_id}", controller.Update)
	router.Delete("/accounts/{account_id}", controller.Delete)
	return router
}

func accountsTestCall(router chi.Router, method, path, body string) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, reader))
	return recorder
}

// TestDeletingAnAccountThatStillHoldsKeysIsRefused states the guard that keeps
// a gateway API key from outliving its account. An orphaned key keeps
// authenticating, and with no account behind it the gateway falls back to the
// default credential policy: the operator would have revoked an account and
// silently widened what its keys may reach.
func TestDeletingAnAccountThatStillHoldsKeysIsRefused(t *testing.T) {
	controller, accounts, keys := newAccountsTestController(t)
	router := accountsTestRouter(controller)

	_, err := accounts.Create(context.Background(), account.Account{ID: "acme", Name: "Acme", Active: true})
	require.NoError(t, err)
	_, err = keys.Create(context.Background(), identity.APIKey{
		ID: "key-a", Name: "acme-key", Hash: "hash-a", Scopes: []string{"chat:write"},
		AccountID: "acme", Active: true,
	})
	require.NoError(t, err)

	refused := accountsTestCall(router, http.MethodDelete, "/accounts/acme", "")
	require.Equal(t, http.StatusConflict, refused.Code)
	assert.Contains(t, refused.Body.String(), "still holds gateway API keys")

	// The account is still there to be reassigned.
	require.Equal(t, http.StatusOK, accountsTestCall(router, http.MethodGet, "/accounts/acme", "").Code)

	// Once no key names it, the same delete succeeds.
	keyRecord, err := keys.GetByID(context.Background(), "key-a")
	require.NoError(t, err)
	require.NoError(t, keys.Delete(context.Background(), "key-a", keyRecord.Revision))
	require.Equal(t, http.StatusOK, accountsTestCall(router, http.MethodDelete, "/accounts/acme", "").Code)
	require.Equal(t, http.StatusNotFound, accountsTestCall(router, http.MethodGet, "/accounts/acme", "").Code)
}

// TestDeletingTheDefaultAccountIsRefused covers the account every key without
// an explicit account resolves to. Deleting it would orphan all of them at
// once.
func TestDeletingTheDefaultAccountIsRefused(t *testing.T) {
	controller, _, _ := newAccountsTestController(t)

	recorder := accountsTestCall(accountsTestRouter(controller), http.MethodDelete, "/accounts/"+account.DefaultID, "")
	require.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "default account cannot be deleted")
}

// TestAccountUpdateIsRevisionChecked proves the update is a read-modify-write
// at the revision it read, not a last-write-wins overwrite. Two operators
// editing one account's caps must not have one cap silently disappear.
func TestAccountUpdateIsRevisionChecked(t *testing.T) {
	accounts, err := account.Open(storage.NewMockStore())
	require.NoError(t, err)
	_, err = accounts.EnsureDefault(context.Background())
	require.NoError(t, err)
	_, err = accounts.Create(context.Background(), account.Account{ID: "acme", Name: "Acme", Active: true})
	require.NoError(t, err)

	// A concurrent operator writes between this request's read and its write.
	racing := &racingAccountRepository{Repository: accounts}
	router := accountsTestRouter(NewAccountsController(racing, nil))

	recorder := accountsTestCall(router, http.MethodPut, "/accounts/acme", `{"name":"Acme Corp"}`)
	require.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Account changed during update")
}

// racingAccountRepository writes one competing update between a read and the
// write that follows it, which is the interleaving a revision check exists for.
type racingAccountRepository struct {
	account.Repository
	raced bool
}

func (r *racingAccountRepository) GetByID(ctx context.Context, accountID string) (account.Record, error) {
	record, err := r.Repository.GetByID(ctx, accountID)
	if err != nil || r.raced {
		return record, err
	}
	r.raced = true

	competing := record.Account
	competing.Name = "Written by another operator"
	if _, updateErr := r.Repository.Update(ctx, competing, record.Revision); updateErr != nil {
		return account.Record{}, updateErr
	}
	return record, nil
}

// TestCreatedAccountReportsTheStrategyItRunsUnder covers the read contract an
// operator UI depends on: an account created without a credential strategy is
// reported with the one it actually runs under, never with an empty value the
// caller would have to interpret.
func TestCreatedAccountReportsTheStrategyItRunsUnder(t *testing.T) {
	controller, _, _ := newAccountsTestController(t)
	router := accountsTestRouter(controller)

	created := accountsTestCall(router, http.MethodPost, "/accounts", `{"id":"acme","name":"Acme"}`)
	require.Equal(t, http.StatusCreated, created.Code)

	var body account.Account
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &body))
	assert.Equal(t, "acme", body.ID)
	assert.True(t, body.Active, "a new account is usable without a second call")
	assert.Equal(t, account.Account{}.EffectiveCredentialStrategy(), body.CredentialStrategy)

	fetched := accountsTestCall(router, http.MethodGet, "/accounts/acme", "")
	require.Equal(t, http.StatusOK, fetched.Code)
	var reread account.Account
	require.NoError(t, json.Unmarshal(fetched.Body.Bytes(), &reread))
	assert.Equal(t, body.CredentialStrategy, reread.CredentialStrategy)

	duplicate := accountsTestCall(router, http.MethodPost, "/accounts", `{"id":"acme"}`)
	assert.Equal(t, http.StatusConflict, duplicate.Code)
}

// TestAccountPlaneWithoutStorageRefusesRatherThanReportingNoAccounts covers a
// deployment with no account storage. An empty list would read as "this
// deployment has no accounts", which is a different and wrong answer.
func TestAccountPlaneWithoutStorageRefusesRatherThanReportingNoAccounts(t *testing.T) {
	router := accountsTestRouter(NewAccountsController(nil, nil))

	recorder := accountsTestCall(router, http.MethodGet, "/accounts", "")
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Account management is not configured")
}

// TestAccountPolicyRoundTripsThroughTheWriteSurface covers the operator's
// two policy dials: which providers an account may bring its own credential
// for, and which providers and models it may reach at all.
func TestAccountPolicyRoundTripsThroughTheWriteSurface(t *testing.T) {
	controller, _, _ := newAccountsTestController(t)
	router := accountsTestRouter(controller)

	recorder := accountsTestCall(router, http.MethodPost, "/accounts", `{
		"id": "acme",
		"byok_policy": {"mode": "selected", "providers": ["openai"]},
		"access": [
			{"provider": "openai"},
			{"provider": "groq", "models": ["llama-3.3-70b"]}
		]
	}`)
	require.Equal(t, http.StatusCreated, recorder.Code)

	var created account.Account
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	require.NotNil(t, created.BYOKPolicy)
	assert.Equal(t, account.BYOKSelected, created.BYOKPolicy.Mode)
	assert.Equal(t, []string{"openai"}, created.BYOKPolicy.Providers)
	require.Len(t, created.Access, 2)
	assert.Equal(t, "groq", created.Access[1].Provider)
	assert.Equal(t, []string{"llama-3.3-70b"}, created.Access[1].Models)

	// An all-providers policy and an empty access list both mean "no
	// restriction", so writing them clears the stored policy.
	recorder = accountsTestCall(router, http.MethodPut, "/accounts/acme",
		`{"byok_policy": {"mode": "all"}, "access": []}`)
	require.Equal(t, http.StatusOK, recorder.Code)
	var updated account.Account
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &updated))
	assert.Nil(t, updated.BYOKPolicy)
	assert.Empty(t, updated.Access)

	// An invalid policy is the caller's mistake, refused before storage.
	recorder = accountsTestCall(router, http.MethodPost, "/accounts",
		`{"id": "beta", "byok_policy": {"mode": "selected"}}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	recorder = accountsTestCall(router, http.MethodPost, "/accounts",
		`{"id": "gamma", "access": [{"provider": "openai"}, {"provider": "openai"}]}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
