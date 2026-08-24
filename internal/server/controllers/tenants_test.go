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

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/tenant"
)

// newTenantsTestController wires the account plane over real storage. The
// guards under test are about what the repositories actually hold, so a fake
// repository would prove nothing here.
func newTenantsTestController(t *testing.T) (*TenantsController, tenant.Repository, identity.Repository) {
	t.Helper()
	tenants, err := tenant.Open(storage.NewMockStore())
	require.NoError(t, err)
	_, err = tenants.EnsureDefault(context.Background())
	require.NoError(t, err)

	keys, err := identity.Open(storage.NewMockStore())
	require.NoError(t, err)

	return NewTenantsController(tenants, keys), tenants, keys
}

func tenantsTestRouter(controller *TenantsController) chi.Router {
	router := chi.NewRouter()
	router.Get("/tenants", controller.List)
	router.Post("/tenants", controller.Create)
	router.Get("/tenants/{tenant_id}", controller.Get)
	router.Put("/tenants/{tenant_id}", controller.Update)
	router.Delete("/tenants/{tenant_id}", controller.Delete)
	return router
}

func tenantsTestCall(router chi.Router, method, path, body string) *httptest.ResponseRecorder {
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
	controller, tenants, keys := newTenantsTestController(t)
	router := tenantsTestRouter(controller)

	_, err := tenants.Create(context.Background(), tenant.Tenant{ID: "acme", Name: "Acme", Active: true})
	require.NoError(t, err)
	_, err = keys.Create(context.Background(), identity.APIKey{
		ID: "key-a", Name: "acme-key", Hash: "hash-a", Scopes: []string{"chat:write"},
		TenantID: "acme", Active: true,
	})
	require.NoError(t, err)

	refused := tenantsTestCall(router, http.MethodDelete, "/tenants/acme", "")
	require.Equal(t, http.StatusConflict, refused.Code)
	assert.Contains(t, refused.Body.String(), "still holds gateway API keys")

	// The account is still there to be reassigned.
	require.Equal(t, http.StatusOK, tenantsTestCall(router, http.MethodGet, "/tenants/acme", "").Code)

	// Once no key names it, the same delete succeeds.
	keyRecord, err := keys.GetByID(context.Background(), "key-a")
	require.NoError(t, err)
	require.NoError(t, keys.Delete(context.Background(), "key-a", keyRecord.Revision))
	require.Equal(t, http.StatusOK, tenantsTestCall(router, http.MethodDelete, "/tenants/acme", "").Code)
	require.Equal(t, http.StatusNotFound, tenantsTestCall(router, http.MethodGet, "/tenants/acme", "").Code)
}

// TestDeletingTheDefaultAccountIsRefused covers the account every key without
// an explicit account resolves to. Deleting it would orphan all of them at
// once.
func TestDeletingTheDefaultAccountIsRefused(t *testing.T) {
	controller, _, _ := newTenantsTestController(t)

	recorder := tenantsTestCall(tenantsTestRouter(controller), http.MethodDelete, "/tenants/"+tenant.DefaultID, "")
	require.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "default account cannot be deleted")
}

// TestAccountUpdateIsRevisionChecked proves the update is a read-modify-write
// at the revision it read, not a last-write-wins overwrite. Two operators
// editing one account's caps must not have one cap silently disappear.
func TestAccountUpdateIsRevisionChecked(t *testing.T) {
	tenants, err := tenant.Open(storage.NewMockStore())
	require.NoError(t, err)
	_, err = tenants.EnsureDefault(context.Background())
	require.NoError(t, err)
	_, err = tenants.Create(context.Background(), tenant.Tenant{ID: "acme", Name: "Acme", Active: true})
	require.NoError(t, err)

	// A concurrent operator writes between this request's read and its write.
	racing := &racingTenantRepository{Repository: tenants}
	router := tenantsTestRouter(NewTenantsController(racing, nil))

	recorder := tenantsTestCall(router, http.MethodPut, "/tenants/acme", `{"name":"Acme Corp"}`)
	require.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Account changed during update")
}

// racingTenantRepository writes one competing update between a read and the
// write that follows it, which is the interleaving a revision check exists for.
type racingTenantRepository struct {
	tenant.Repository
	raced bool
}

func (r *racingTenantRepository) GetByID(ctx context.Context, tenantID string) (tenant.Record, error) {
	record, err := r.Repository.GetByID(ctx, tenantID)
	if err != nil || r.raced {
		return record, err
	}
	r.raced = true

	competing := record.Tenant
	competing.Name = "Written by another operator"
	if _, updateErr := r.Repository.Update(ctx, competing, record.Revision); updateErr != nil {
		return tenant.Record{}, updateErr
	}
	return record, nil
}

// TestCreatedAccountReportsTheStrategyItRunsUnder covers the read contract an
// operator UI depends on: an account created without a credential strategy is
// reported with the one it actually runs under, never with an empty value the
// caller would have to interpret.
func TestCreatedAccountReportsTheStrategyItRunsUnder(t *testing.T) {
	controller, _, _ := newTenantsTestController(t)
	router := tenantsTestRouter(controller)

	created := tenantsTestCall(router, http.MethodPost, "/tenants", `{"id":"acme","name":"Acme"}`)
	require.Equal(t, http.StatusCreated, created.Code)

	var account tenant.Tenant
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &account))
	assert.Equal(t, "acme", account.ID)
	assert.True(t, account.Active, "a new account is usable without a second call")
	assert.Equal(t, tenant.Tenant{}.EffectiveCredentialStrategy(), account.CredentialStrategy)

	fetched := tenantsTestCall(router, http.MethodGet, "/tenants/acme", "")
	require.Equal(t, http.StatusOK, fetched.Code)
	var reread tenant.Tenant
	require.NoError(t, json.Unmarshal(fetched.Body.Bytes(), &reread))
	assert.Equal(t, account.CredentialStrategy, reread.CredentialStrategy)

	duplicate := tenantsTestCall(router, http.MethodPost, "/tenants", `{"id":"acme"}`)
	assert.Equal(t, http.StatusConflict, duplicate.Code)
}

// TestAccountPlaneWithoutStorageRefusesRatherThanReportingNoAccounts covers a
// deployment with no account storage. An empty list would read as "this
// deployment has no accounts", which is a different and wrong answer.
func TestAccountPlaneWithoutStorageRefusesRatherThanReportingNoAccounts(t *testing.T) {
	router := tenantsTestRouter(NewTenantsController(nil, nil))

	recorder := tenantsTestCall(router, http.MethodGet, "/tenants", "")
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Account management is not configured")
}
