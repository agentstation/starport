package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	workos "github.com/workos/workos-go/v10"
)

// stubWorkOS plays the WorkOS API: it answers the code exchange with the
// profile the test scripted, so the whole SSO dance runs in-process.
func stubWorkOS(t *testing.T, profile map[string]any, status int) *httptest.Server {
	t.Helper()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/sso/token", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "token",
				"profile":      profile,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "refused"})
	}))
	t.Cleanup(stub.Close)
	return stub
}

func newTestWorkOS(t *testing.T, profile map[string]any, status int) *Authenticator {
	t.Helper()
	stub := stubWorkOS(t, profile, status)
	repositories := newTestRepositories(t)
	path, err := NewAuthenticator(AcquisitionConfig{
		CallbackBaseURL: "http://localhost:8080",
		WorkOS: WorkOSConfig{
			APIKey:       "sk_test",
			ClientID:     "client_01",
			Organization: "org_01H",
			Endpoint:     stub.URL,
		},
	}, repositories.Users)
	require.NoError(t, err)
	return path
}

// TestWorkOSSSOEndToEnd is the acceptance test for the second acquisition
// path: a WorkOS-brokered arrival goes from Begin through the callback to a
// redeemed subject, and the person lands in the same user model the OAuth
// path fills.
func TestWorkOSSSOEndToEnd(t *testing.T) {
	path := newTestWorkOS(t, map[string]any{
		"id":         "prof_01HXE",
		"email":      "person@enterprise.example.com",
		"first_name": "Enterprise",
		"last_name":  "Person",
	}, http.StatusOK)

	claim := beginAndCallback(t, path, "workos")

	subject, err := path.Authenticate(claim)
	require.NoError(t, err)
	require.Equal(t, "workos:prof_01HXE", subject)

	record, err := path.users.GetBySubject(context.Background(), subject)
	require.NoError(t, err)
	require.Equal(t, "person@enterprise.example.com", record.User.Email)
	require.Equal(t, "Enterprise Person", record.User.DisplayName)
}

// TestWorkOSAuthorizeURLCarriesTheConfig pins what begin sends the browser
// away with: the operator's client, the organization selector, the exact
// callback this gateway serves, and a state to bring back.
func TestWorkOSAuthorizeURLCarriesTheConfig(t *testing.T) {
	path := newTestWorkOS(t, map[string]any{"id": "prof_x"}, http.StatusOK)

	begin := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/console/identity/workos", nil)
	require.NoError(t, path.Begin(begin, request, "workos"))
	require.Equal(t, http.StatusTemporaryRedirect, begin.Code)

	authorize, err := url.Parse(begin.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/sso/authorize", authorize.Path)
	query := authorize.Query()
	require.Equal(t, "code", query.Get("response_type"))
	require.Equal(t, "client_01", query.Get("client_id"))
	require.Equal(t, "org_01H", query.Get("organization"))
	require.Equal(t, "http://localhost:8080"+CallbackPath("workos"), query.Get("redirect_uri"))
	require.NotEmpty(t, query.Get("state"))
}

// TestWorkOSCallbackStateMustMatch pins this path's CSRF guard: a callback
// whose state is not the one begin issued never reaches the user model.
func TestWorkOSCallbackStateMustMatch(t *testing.T) {
	path := newTestWorkOS(t, map[string]any{"id": "prof_csrf"}, http.StatusOK)

	begin := httptest.NewRecorder()
	require.NoError(t, path.Begin(begin,
		httptest.NewRequest(http.MethodGet, "/console/identity/workos", nil), "workos"))

	callback := httptest.NewRequest(http.MethodGet,
		CallbackPath("workos")+"?state=forged&code=granted", nil)
	for _, cookie := range begin.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	_, err := path.Complete(httptest.NewRecorder(), callback, "workos")
	require.Error(t, err)

	listed, err := path.users.List(context.Background(), 10, 0)
	require.NoError(t, err)
	require.Empty(t, listed)
}

// TestWorkOSCallbackNeedsItsCookie covers the arrival with no dance behind
// it: a bare callback carries no state cookie and is refused before any
// exchange.
func TestWorkOSCallbackNeedsItsCookie(t *testing.T) {
	path := newTestWorkOS(t, map[string]any{"id": "prof_bare"}, http.StatusOK)

	callback := httptest.NewRequest(http.MethodGet,
		CallbackPath("workos")+"?state=anything&code=granted", nil)
	_, err := path.Complete(httptest.NewRecorder(), callback, "workos")
	require.Error(t, err)
}

// TestWorkOSRefusedExchangeIsRefused covers WorkOS saying no: a code the
// broker will not exchange never becomes a user or a claim.
func TestWorkOSRefusedExchangeIsRefused(t *testing.T) {
	path := newTestWorkOS(t, nil, http.StatusUnauthorized)

	begin := httptest.NewRecorder()
	require.NoError(t, path.Begin(begin,
		httptest.NewRequest(http.MethodGet, "/console/identity/workos", nil), "workos"))
	authorize, err := url.Parse(begin.Header().Get("Location"))
	require.NoError(t, err)

	callback := httptest.NewRequest(http.MethodGet,
		CallbackPath("workos")+"?state="+url.QueryEscape(authorize.Query().Get("state"))+"&code=bad", nil)
	for _, cookie := range begin.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	_, err = path.Complete(httptest.NewRecorder(), callback, "workos")
	require.Error(t, err)

	listed, err := path.users.List(context.Background(), 10, 0)
	require.NoError(t, err)
	require.Empty(t, listed)
}

// TestWorkOSDisplayName pins the fold from the directory's name fields to
// the one display name the user model carries.
func TestWorkOSDisplayName(t *testing.T) {
	full := "Full Name"
	first := "First"
	last := "Last"
	blank := "  "
	require.Equal(t, "Full Name", workosDisplayName(&workos.Profile{
		Name: &full, FirstName: &first, LastName: &last,
	}))
	require.Equal(t, "First Last", workosDisplayName(&workos.Profile{
		Name: &blank, FirstName: &first, LastName: &last,
	}))
	require.Equal(t, "Last", workosDisplayName(&workos.Profile{LastName: &last}))
	require.Equal(t, "", workosDisplayName(&workos.Profile{}))
}
