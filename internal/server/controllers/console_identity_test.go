package controllers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/localauth"
)

// authenticatorStub plays the acquisition path: Begin redirects like a
// consent page would, Complete returns a claim, and Authenticate redeems it
// once — enough behavior to prove the controller's side of the contract.
type authenticatorStub struct {
	providers   []string
	claim       string
	subject     string
	completeErr error
	redeemed    bool
}

func (a *authenticatorStub) Providers() []string { return a.providers }

func (a *authenticatorStub) Begin(w http.ResponseWriter, r *http.Request, provider string) error {
	if a.completeErr != nil {
		return a.completeErr
	}
	http.Redirect(w, r, "https://provider.test/consent?state=abc", http.StatusTemporaryRedirect)
	return nil
}

func (a *authenticatorStub) Complete(http.ResponseWriter, *http.Request, string) (string, error) {
	if a.completeErr != nil {
		return "", a.completeErr
	}
	return a.claim, nil
}

func (a *authenticatorStub) Authenticate(claim string) (string, error) {
	if a.redeemed || claim != a.claim {
		return "", errors.New("identity claim is invalid or expired")
	}
	a.redeemed = true
	return a.subject, nil
}

// identityRoutes mounts the controller the way routes.go does, so the
// {provider} URL parameter resolves.
func identityRoutes(controller *ConsoleIdentityController) *chi.Mux {
	mux := chi.NewMux()
	mux.Get("/console/identity/providers", controller.Providers)
	mux.Get("/console/identity/{provider}", controller.Begin)
	mux.Get("/console/identity/{provider}/callback", controller.Callback)
	return mux
}

func identityGet(mux *chi.Mux, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "127.0.0.1:52011"
	mux.ServeHTTP(recorder, request)
	return recorder
}

// TestAnIdentityCallbackOpensASession is the controller's acceptance test:
// the provider's callback becomes the same console session every other grant
// mints, carrying the subject the acquisition path resolved.
func TestAnIdentityCallbackOpensASession(t *testing.T) {
	token := launchToken(t, 1)
	gate := localauth.NewGate(token, "127.0.0.1")
	stub := &authenticatorStub{
		providers: []string{"google"},
		claim:     "one-time-claim",
		subject:   "google:114380",
	}
	gate.UseIdentityProvider(stub)
	mux := identityRoutes(NewConsoleIdentityController(stub, gate))

	recorder := identityGet(mux, "/console/identity/google/callback?state=abc&code=ok")
	response := recorder.Result()

	require.Equal(t, http.StatusSeeOther, response.StatusCode)
	assert.Equal(t, "/", response.Header.Get("Location"))
	assert.NotEmpty(t, markerCookie(response))

	var sessionValue string
	for _, cookie := range response.Cookies() {
		if cookie.Name == localauth.SessionCookie {
			sessionValue = cookie.Value
		}
	}
	require.NotEmpty(t, sessionValue)
	session, err := gate.Verify(sessionValue, time.Now())
	require.NoError(t, err)
	assert.Equal(t, localauth.GrantIdentity, session.Grant)
	assert.Equal(t, "google:114380", session.Subject)
}

// TestAnUnconfiguredDeploymentAnswersTheOperator holds the nil state: the
// routes exist, the provider list is empty, and starting a dance names the
// operator's lever rather than pretending the surface is absent.
func TestAnUnconfiguredDeploymentAnswersTheOperator(t *testing.T) {
	gate := localauth.NewGate(launchToken(t, 1), "127.0.0.1")
	mux := identityRoutes(NewConsoleIdentityController(nil, gate))

	listed := identityGet(mux, "/console/identity/providers")
	require.Equal(t, http.StatusOK, listed.Code)
	assert.JSONEq(t, `{"providers": []}`, listed.Body.String())

	began := identityGet(mux, "/console/identity/google")
	assert.Equal(t, http.StatusServiceUnavailable, began.Code)
	assert.Contains(t, refusalMessage(t, began.Result()), "IDENTITY_OAUTH_")

	called := identityGet(mux, "/console/identity/google/callback")
	assert.Equal(t, http.StatusServiceUnavailable, called.Code)
}

// TestProvidersAreListedForFirstContact pins what the console reads before
// drawing its buttons.
func TestProvidersAreListedForFirstContact(t *testing.T) {
	gate := localauth.NewGate(launchToken(t, 1), "127.0.0.1")
	stub := &authenticatorStub{providers: []string{"github", "google"}}
	mux := identityRoutes(NewConsoleIdentityController(stub, gate))

	listed := identityGet(mux, "/console/identity/providers")
	require.Equal(t, http.StatusOK, listed.Code)
	assert.JSONEq(t, `{"providers": ["github", "google"]}`, listed.Body.String())
}

// TestBeginRedirectsToTheProvider covers the outbound half of the dance.
func TestBeginRedirectsToTheProvider(t *testing.T) {
	gate := localauth.NewGate(launchToken(t, 1), "127.0.0.1")
	stub := &authenticatorStub{providers: []string{"google"}}
	mux := identityRoutes(NewConsoleIdentityController(stub, gate))

	began := identityGet(mux, "/console/identity/google")
	require.Equal(t, http.StatusTemporaryRedirect, began.Code)
	assert.Contains(t, began.Header().Get("Location"), "provider.test/consent")
}

// TestAFailedDanceIsRefusedAndClearsCookies covers the refusal path: one
// message, the cause logged not sent, and any session cookies cleared so the
// console does not keep claiming a session that does not verify.
func TestAFailedDanceIsRefusedAndClearsCookies(t *testing.T) {
	gate := localauth.NewGate(launchToken(t, 1), "127.0.0.1")
	stub := &authenticatorStub{
		providers:   []string{"google"},
		completeErr: errors.New("state mismatch"),
	}
	mux := identityRoutes(NewConsoleIdentityController(stub, gate))

	recorder := identityGet(mux, "/console/identity/google/callback?state=forged")
	response := recorder.Result()

	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	message := refusalMessage(t, response)
	assert.NotContains(t, message, "state mismatch",
		"the cause is for the log, not the browser")
	cleared := false
	for _, cookie := range response.Cookies() {
		if cookie.Name == localauth.SessionCookie && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	assert.True(t, cleared, "a refusal clears the session cookie")
}

// TestAReplayedClaimIsRefused holds the one-time property end to end at the
// controller: the second callback carrying the same claim mints nothing.
func TestAReplayedClaimIsRefused(t *testing.T) {
	gate := localauth.NewGate(launchToken(t, 1), "127.0.0.1")
	stub := &authenticatorStub{
		providers: []string{"google"},
		claim:     "spent-once",
		subject:   "google:1",
	}
	gate.UseIdentityProvider(stub)
	mux := identityRoutes(NewConsoleIdentityController(stub, gate))

	first := identityGet(mux, "/console/identity/google/callback?state=abc")
	require.Equal(t, http.StatusSeeOther, first.Code)

	second := identityGet(mux, "/console/identity/google/callback?state=abc")
	require.Equal(t, http.StatusUnauthorized, second.Code)
}

// The stub must satisfy the contract the composition root hands across.
var _ IdentityAuthenticator = (*authenticatorStub)(nil)
