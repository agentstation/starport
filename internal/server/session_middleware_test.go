package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
)

// sessionHarness is a middleware over an empty identity store. Empty is the
// point: nothing in these tests is meant to authenticate as a gateway API key,
// so a request that passes did so on its session alone.
func sessionHarness(t *testing.T, gate *localauth.Gate) *AuthMiddleware {
	t.Helper()
	identities, err := identity.Open(storage.NewMockStore())
	require.NoError(t, err)
	middleware := NewAuthMiddleware(identities)
	middleware.AcceptSessions(gate)
	return middleware
}

// openSession mints a session cookie value the given token signed.
func openSession(t *testing.T, token localauth.Token) string {
	t.Helper()
	value, _, err := localauth.IssueSession(token, time.Now())
	require.NoError(t, err)
	return value
}

func sessionToken(t *testing.T, generation uint64) localauth.Token {
	t.Helper()
	token, err := localauth.Mint(generation, time.Now())
	require.NoError(t, err)
	return token
}

// callWithSession sends one request carrying only a session cookie and reports
// the status plus the identity the handler saw.
func callWithSession(
	middleware *AuthMiddleware,
	cookie string,
) (int, *identity.APIKey, string, bool) {
	var seen *identity.APIKey
	var seenTenant string
	var seenSecret bool
	handler := middleware.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = requestctx.GetAPIKeyModel(r.Context())
		seenTenant = requestctx.TenantIDOrDefault(r.Context())
		_, seenSecret = requestctx.GetAPIKey(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: cookie})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, seen, seenTenant, seenSecret
}

func TestASessionAuthenticatesWithNoBearerKey(t *testing.T) {
	token := sessionToken(t, 2)
	middleware := sessionHarness(t, localauth.NewGate(token))

	status, seen, tenantID, carriedSecret := callWithSession(middleware, openSession(t, token))

	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, seen)
	assert.Equal(t, identity.LocalOperatorKeyID, seen.ID)
	assert.True(t, seen.HasScope("admin"), "a session holder is the machine's operator")
	assert.Equal(t, "default", tenantID)
	// Nothing downstream may believe a bearer key was presented. A reader that
	// found one here would be holding the session cookie and free to forward it.
	assert.False(t, carriedSecret, "a session must not appear as a gateway API key")
}

func TestRotatingTheLocalTokenEndsALiveSession(t *testing.T) {
	// The operator's revocation story: `starport auth rotate` writes a new
	// secret, and every browser signed in under the old one stops being signed
	// in, with no session list to clear and no expiry to wait out.
	before := sessionToken(t, 2)
	cookie := openSession(t, before)

	middleware := sessionHarness(t, localauth.NewGate(sessionToken(t, 3)))
	status, seen, _, _ := callWithSession(middleware, cookie)

	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Nil(t, seen)
}

func TestAnExpiredSessionStopsAuthenticating(t *testing.T) {
	token := sessionToken(t, 2)
	value, _, err := localauth.IssueSession(token, time.Now().Add(-localauth.SessionTTL-time.Minute))
	require.NoError(t, err)

	status, seen, _, _ := callWithSession(sessionHarness(t, localauth.NewGate(token)), value)

	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Nil(t, seen)
}

func TestAGatewayWithNoGateIgnoresSessionCookies(t *testing.T) {
	status, seen, _, _ := callWithSession(sessionHarness(t, nil), "anything.at-all")

	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Nil(t, seen)
}

func TestAnUnusableSessionSaysHowToOpenANewOne(t *testing.T) {
	// The two refusals are deliberately different. A caller with no credential
	// has not signed in; a caller with a stale cookie has one to replace, and
	// only the second should be told to run `starport ui`.
	middleware := sessionHarness(t, localauth.NewGate(sessionToken(t, 3)))
	handler := middleware.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	stale := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	stale.AddCookie(&http.Cookie{
		Name:  localauth.SessionCookie,
		Value: openSession(t, sessionToken(t, 2)),
	})
	staleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(staleRecorder, stale)
	assert.Equal(t, http.StatusUnauthorized, staleRecorder.Code)
	assert.Contains(t, staleRecorder.Body.String(), "starport ui")

	bare := httptest.NewRecorder()
	handler.ServeHTTP(bare, httptest.NewRequest(http.MethodGet, "/api/v1/models", nil))
	assert.Equal(t, http.StatusUnauthorized, bare.Code)
	assert.Contains(t, bare.Body.String(), "Missing API key")
}

func TestAnExplicitKeyBeatsAnAmbientSession(t *testing.T) {
	// A cookie is attached by the browser; an Authorization header is something
	// the caller chose to send. When both arrive, the deliberate one decides —
	// otherwise a stale session would silently override a key a client set.
	token := sessionToken(t, 2)
	middleware := sessionHarness(t, localauth.NewGate(token))
	handler := middleware.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: openSession(t, token)})
	request.Header.Set("Authorization", "Bearer STARPORT_not-a-real-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Invalid API key")
}
