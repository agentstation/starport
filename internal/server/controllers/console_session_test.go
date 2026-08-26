package controllers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/localauth"
)

// paste posts one value at the route the way a browser would, from the address
// the request appears to arrive from.
func paste(
	t *testing.T, controller *ConsoleSessionController, body, remoteAddr string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/console/session", strings.NewReader(body))
	request.RemoteAddr = remoteAddr
	controller.Create(recorder, request)
	return recorder
}

// markerCookie returns the readable marker a response set, or "".
func markerCookie(response *http.Response) string {
	for _, cookie := range response.Cookies() {
		if cookie.Name == localauth.SessionMarkerCookie && cookie.MaxAge >= 0 {
			return cookie.Value
		}
	}
	return ""
}

func refusalMessage(t *testing.T, response *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var body map[string]string
	require.NoError(t, json.Unmarshal(raw, &body))
	return body["message"]
}

// TestAPastedTokenOpensASession is the asymmetry this campaign closes, now
// reachable from a browser. The two cookies have opposite jobs and the test
// asserts both: the credential must be unreadable to scripts, and the marker
// must be readable, or the console cannot render a signed-in shell without
// making a request whose only purpose is to ask whether the next one would
// work.
func TestAPastedTokenOpensASession(t *testing.T) {
	token := launchToken(t, 3)
	controller := NewConsoleSessionController(localauth.NewGate(token, "127.0.0.1"))

	response := paste(t, controller, `{"token":"`+token.Secret+`"}`, "127.0.0.1:54321").Result()
	defer response.Body.Close()

	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	assert.NotEmpty(t, sessionCookie(response), "the paste should hand over a session")
	assert.Equal(t, "1", markerCookie(response))

	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case localauth.SessionCookie:
			assert.True(t, cookie.HttpOnly, "the credential must be unreadable to scripts")
		case localauth.SessionMarkerCookie:
			assert.False(t, cookie.HttpOnly, "the marker exists to be read")
		}
	}
}

// TestAWrongPasteIsRefusedWithOneMessage holds the property at the HTTP
// boundary rather than only in the grant. Every one of these fails a different
// check inside, and a route that let that difference reach the browser would
// tell a guesser which check they had got past.
func TestAWrongPasteIsRefusedWithOneMessage(t *testing.T) {
	token := launchToken(t, 3)
	controller := NewConsoleSessionController(localauth.NewGate(token, "127.0.0.1"))

	messages := map[string]string{}
	for name, body := range map[string]string{
		"a wrong secret":          `{"token":"` + localauth.TokenPrefix + `nope"}`,
		"an empty token":          `{"token":""}`,
		"no token field at all":   `{}`,
		"a body that is not JSON": `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			response := paste(t, controller, body, "127.0.0.1:54321").Result()
			defer response.Body.Close()

			assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
			assert.Empty(t, sessionCookie(response))
			messages[name] = refusalMessage(t, response)
		})
	}

	require.Len(t, messages, 4)
	for name, message := range messages {
		assert.Equal(t, sessionRefusal, message, "%s should get the shared refusal", name)
	}
}

// TestAGatewayAPIKeyIsToldWhichCredentialTheFieldWants is the deliberate
// exception, asserted at the boundary because that is where a reader sees it.
// It must not be the shared message: somebody who pasted a STARPORT_ key made a
// category error, and answering "that value did not work" leaves them to guess
// which of two credentials this field wanted — the confusion the route exists
// to remove.
func TestAGatewayAPIKeyIsToldWhichCredentialTheFieldWants(t *testing.T) {
	controller := NewConsoleSessionController(
		localauth.NewGate(launchToken(t, 3), "127.0.0.1"),
	)

	response := paste(
		t, controller, `{"token":"`+localauth.GatewayAPIKeyPrefix+`abc123"}`, "127.0.0.1:54321",
	).Result()
	defer response.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Empty(t, sessionCookie(response))

	message := refusalMessage(t, response)
	assert.NotEqual(t, sessionRefusal, message)
	assert.Contains(t, message, "starport auth token")
}

// TestARefusedPasteClearsAStaleSession covers the case an operator actually hits:
// the token was rotated, the browser is still carrying a cookie from the old
// one, and the paste fails. Leaving that cookie in place would let the console
// keep rendering a signed-in shell while every request behind it is refused.
func TestARefusedPasteClearsAStaleSession(t *testing.T) {
	controller := NewConsoleSessionController(
		localauth.NewGate(launchToken(t, 3), "127.0.0.1"),
	)

	response := paste(t, controller, `{"token":"wrong"}`, "127.0.0.1:54321").Result()
	defer response.Body.Close()

	cleared := map[string]bool{}
	for _, cookie := range response.Cookies() {
		if cookie.MaxAge < 0 {
			cleared[cookie.Name] = true
		}
	}
	assert.True(t, cleared[localauth.SessionCookie], "the credential cookie should be expired")
	assert.True(t, cleared[localauth.SessionMarkerCookie], "the marker should be expired too")
}

// TestAnUnrotatedTokenIsRefusedFromOffMachine is the reverse-proxy case,
// asserted through the route because the route is what decides which address
// the grant judges. Reading a forwarded header here instead of the peer address
// would hand the console to anyone who can set one.
func TestAnUnrotatedTokenIsRefusedFromOffMachine(t *testing.T) {
	token := launchToken(t, 3)
	controller := NewConsoleSessionController(localauth.NewGate(token, "127.0.0.1"))
	body := `{"token":"` + token.Secret + `"}`

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/console/session", strings.NewReader(body))
	request.RemoteAddr = "203.0.113.7:41234"
	// The header a proxy would set, and the value a caller can forge. It must
	// change nothing.
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	controller.Create(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Empty(t, sessionCookie(response))

	// The same secret from the machine itself still works, so the refusal is
	// about where the caller is and not about the secret.
	local := paste(t, controller, body, "127.0.0.1:54321").Result()
	defer local.Body.Close()
	assert.Equal(t, http.StatusNoContent, local.StatusCode)
}

// TestABuildWithNoLocalTokenRefusesRatherThan404s holds the shape the launch
// route already has. A 404 would say this build has no console sign-in, which
// is a different and false statement.
func TestABuildWithNoLocalTokenRefusesRatherThan404s(t *testing.T) {
	controller := NewConsoleSessionController(nil)

	response := paste(t, controller, `{"token":"anything"}`, "127.0.0.1:54321").Result()
	defer response.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Equal(t, sessionRefusal, refusalMessage(t, response))
}

// TestTheSessionExpiresWithTheToken checks the route does not invent a lifetime
// of its own. Both grants have to produce the same session, or "sign out"
// would mean different things depending on how a browser got in.
func TestTheSessionExpiresWithTheToken(t *testing.T) {
	token := launchToken(t, 3)
	at := time.Now()
	controller := &ConsoleSessionController{
		gate: localauth.NewGate(token, "127.0.0.1"),
		now:  func() time.Time { return at },
	}

	response := paste(t, controller, `{"token":"`+token.Secret+`"}`, "127.0.0.1:54321").Result()
	defer response.Body.Close()

	verified, err := localauth.VerifySession(sessionCookie(response), token, at)
	require.NoError(t, err)
	assert.Equal(t, localauth.GrantLocalToken, verified.Grant)
	assert.Empty(t, verified.Subject, "a machine-local grant knows where you are, not who")
	assert.WithinDuration(t, at.Add(localauth.SessionTTL), verified.ExpiresAt, time.Second)
}
