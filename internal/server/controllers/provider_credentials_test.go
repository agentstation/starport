package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The routes themselves, the two scopes they address, and the isolation
// between them are proven end to end over real storage in the server package
// (internal/server/credential_routes_test.go). What lives here is what that
// suite cannot reach through a wired gateway: the controller's behavior when
// the credential store is absent, when the body is not a credential, and when
// the store rejects a value.

// mockKeyManager implements keyring.ProviderKeys for testing. Every method
// succeeds, so a test overrides only the one behavior it is about.
type mockKeyManager struct{}

func (m *mockKeyManager) AddKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback bool, priority int) (*credentials.ProviderKey, error) {
	return &credentials.ProviderKey{
		Scope:    scope,
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) GetKey(ctx context.Context, scope, provider string) (*credentials.ProviderKey, error) {
	return &credentials.ProviderKey{
		Scope:    scope,
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) GetKeys(ctx context.Context, scope, provider string) ([]*credentials.ProviderKey, error) {
	return []*credentials.ProviderKey{
		{
			Scope:    scope,
			Provider: provider,
		},
	}, nil
}

func (m *mockKeyManager) ListKeys(ctx context.Context, scope string) ([]*credentials.ProviderKey, error) {
	return []*credentials.ProviderKey{
		{
			Scope:    scope,
			Provider: "openai",
		},
	}, nil
}

func (m *mockKeyManager) UpdateKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback *bool, priority *int) (*credentials.ProviderKey, error) {
	return &credentials.ProviderKey{
		Scope:    scope,
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) DeleteKey(ctx context.Context, scope, provider string) error {
	return nil
}

func (m *mockKeyManager) ValidateKey(ctx context.Context, provider string, key map[string]string, config map[string]any) error {
	return nil
}

func (m *mockKeyManager) AddSharedCredential(ctx context.Context, provider string, key map[string]string, config map[string]any, params keyring.SharedCredentialParams) (*credentials.SharedCredential, error) {
	return &credentials.SharedCredential{ID: "shared-1", Access: credentials.AccessOpen}, nil
}

func (m *mockKeyManager) GetSharedCredentials(ctx context.Context, provider string) ([]credentials.SharedCredential, error) {
	return []credentials.SharedCredential{{ID: "shared-1", Access: credentials.AccessOpen}}, nil
}

func (m *mockKeyManager) UpdateSharedCredential(ctx context.Context, provider, credentialID string, update keyring.SharedCredentialUpdate) (*credentials.SharedCredential, error) {
	return &credentials.SharedCredential{ID: credentialID, Access: credentials.AccessOpen}, nil
}

func (m *mockKeyManager) DeleteSharedCredential(ctx context.Context, provider, credentialID string) error {
	return nil
}

func (m *mockKeyManager) ListShared(ctx context.Context) ([]*credentials.ProviderKey, error) {
	return []*credentials.ProviderKey{
		{
			Scope:    keyring.SharedScope,
			Provider: "openai",
		},
	}, nil
}

func (m *mockKeyManager) ResolveStoredMaterial(context.Context, string, catalogs.Provider) (credentials.Material, error) {
	return credentials.Material{}, nil
}

func (m *mockKeyManager) ResolveSharedMaterial(context.Context, string, catalogs.Provider) (credentials.Material, error) {
	return credentials.Material{}, nil
}

func (m *mockKeyManager) RecordUsage(ctx context.Context, scope string, provider string, usage *keyring.Usage) error {
	return nil
}

func (m *mockKeyManager) RotateEncryptionKey(ctx context.Context) error {
	return nil
}

// credentialRouter mounts both credential surfaces on one router so a test
// addresses either plane by path, the way a caller does.
func credentialRouter(store keyring.ProviderKeys) http.Handler {
	handler := NewProviderCredentialsController(store)

	router := chi.NewRouter()
	router.Route("/api/v1/providers/{provider}/credentials", func(r chi.Router) {
		r.Get("/", handler.SharedGet)
		r.Put("/", handler.SharedPut)
		r.Delete("/", handler.SharedDelete)
		r.Post("/validate", handler.SharedValidate)
	})
	router.Route("/api/v1/accounts/{account_id}/byok", func(r chi.Router) {
		r.Get("/", handler.BYOKList)
		r.Get("/{provider}", handler.BYOKGet)
		r.Put("/{provider}", handler.BYOKPut)
		r.Delete("/{provider}", handler.BYOKDelete)
		r.Post("/{provider}/validate", handler.BYOKValidate)
	})
	return router
}

func callCredentialRoute(t *testing.T, store keyring.ProviderKeys, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		request.Header.Set("Content-Type", "application/json")
	}

	recorder := httptest.NewRecorder()
	credentialRouter(store).ServeHTTP(recorder, request)
	return recorder
}

func decodeCredentialError(t *testing.T, recorder *httptest.ResponseRecorder) dto.ErrorResponse {
	t.Helper()

	var response dto.ErrorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

// TestCredentialSurfacesDegradeLoudlyWithoutAStore covers the deployment that
// never configured credential storage. Every route on both planes has to say
// so rather than report an empty credential set, which would read as "no
// credential is applied" and send an operator looking in the wrong place.
func TestCredentialSurfacesDegradeLoudlyWithoutAStore(t *testing.T) {
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/providers/openai/credentials", ""},
		{http.MethodPut, "/api/v1/providers/openai/credentials", `{"credentials":{"api-key":"x"}}`},
		{http.MethodDelete, "/api/v1/providers/openai/credentials", ""},
		{http.MethodPost, "/api/v1/providers/openai/credentials/validate", ""},
		{http.MethodGet, "/api/v1/accounts/acme/byok", ""},
		{http.MethodGet, "/api/v1/accounts/acme/byok/openai", ""},
		{http.MethodPut, "/api/v1/accounts/acme/byok/openai", `{"credentials":{"api-key":"x"}}`},
		{http.MethodDelete, "/api/v1/accounts/acme/byok/openai", ""},
		{http.MethodPost, "/api/v1/accounts/acme/byok/openai/validate", ""},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			recorder := callCredentialRoute(t, nil, route.method, route.path, route.body)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			assert.Contains(t, decodeCredentialError(t, recorder).Error.Message,
				"Provider credential management is not configured")
		})
	}
}

// TestACredentialBodyMustCarryStringFields covers the write body. A credential
// field is a secret or a parameter, never a nested object, so a body that is
// shaped differently is the caller's mistake and names the field it got wrong.
func TestACredentialBodyMustCarryStringFields(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		param string
	}{
		{name: "not json", body: `{`},
		{name: "no credentials", body: `{"config":{"region":"us-east-1"}}`, param: "credentials"},
		{name: "empty credentials", body: `{"credentials":{}}`, param: "credentials"},
		{name: "nested value", body: `{"credentials":{"api-key":{"value":"x"}}}`, param: "credentials.api-key"},
		{name: "numeric value", body: `{"credentials":{"api-key":42}}`, param: "credentials.api-key"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Both planes decode the same body, so proving one proves both.
			recorder := callCredentialRoute(t, &mockKeyManager{}, http.MethodPut,
				"/api/v1/accounts/acme/byok/openai", testCase.body)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			response := decodeCredentialError(t, recorder)
			if testCase.param != "" {
				require.NotNil(t, response.Error.Param)
				assert.Equal(t, testCase.param, *response.Error.Param,
					"the caller has to be told which field to fix")
			}
		})
	}
}

// rejectingKeyManager fails writes with the error a test hands it, and reports
// the credential as absent so a PUT takes the create branch.
type rejectingKeyManager struct {
	mockKeyManager
	err error
}

func (m *rejectingKeyManager) GetSharedCredentials(context.Context, string) ([]credentials.SharedCredential, error) {
	return nil, nil
}

func (m *rejectingKeyManager) AddSharedCredential(context.Context, string, map[string]string, map[string]any, keyring.SharedCredentialParams) (*credentials.SharedCredential, error) {
	return nil, m.err
}

// TestASchemaRejectionIsTheCallersMistake separates the two ways a write
// fails. A value that does not satisfy the provider's catalog-declared
// credential schema is a 400 the caller can act on; anything else is a 500 the
// operator has to act on. Collapsing them would tell an operator to check the
// logs for a typo the caller made.
func TestASchemaRejectionIsTheCallersMistake(t *testing.T) {
	t.Run("schema rejection", func(t *testing.T) {
		store := &rejectingKeyManager{err: &keyring.ValidationError{
			Provider: "openai",
			Field:    "api-key",
			Message:  "required credential is missing",
		}}

		recorder := callCredentialRoute(t, store, http.MethodPut,
			"/api/v1/providers/openai/credentials", `{"credentials":{"organization":"acme"}}`)

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		response := decodeCredentialError(t, recorder)
		assert.Contains(t, response.Error.Message, "required credential is missing",
			"the caller has to learn what the schema wanted")
		assert.Contains(t, response.Error.Message, "api-key")
	})

	t.Run("storage failure", func(t *testing.T) {
		store := &rejectingKeyManager{err: errors.New("keyring is sealed")}

		recorder := callCredentialRoute(t, store, http.MethodPut,
			"/api/v1/providers/openai/credentials", `{"credentials":{"api-key":"secret-value-under-test"}}`)

		require.Equal(t, http.StatusInternalServerError, recorder.Code)
		response := decodeCredentialError(t, recorder)
		assert.NotContains(t, response.Error.Message, "secret-value-under-test",
			"a failure report must not echo the credential it failed to store")
		assert.NotContains(t, response.Error.Message, "keyring is sealed",
			"an internal failure stays in the log, not in the response")
	})
}

// TestACredentialResponseNeverCarriesItsSecret guards the read surfaces the
// console renders. The controller only ever sees the encrypted record, so this
// asserts the shape it reports: that a credential exists, never what it is.
func TestACredentialResponseNeverCarriesItsSecret(t *testing.T) {
	recorder := callCredentialRoute(t, &mockKeyManager{}, http.MethodGet,
		"/api/v1/accounts/acme/byok/openai", "")
	require.Equal(t, http.StatusOK, recorder.Code)

	var summary map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &summary))
	assert.Equal(t, "openai", summary["provider"])
	assert.NotContains(t, summary, "credentials")
	assert.NotContains(t, summary, "encrypted_credential")
	assert.NotContains(t, summary, "key")
}
