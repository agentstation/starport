package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/localauth"
)

func launchToken(t *testing.T, generation uint64) localauth.Token {
	t.Helper()
	token, err := localauth.Mint(generation, time.Now())
	require.NoError(t, err)
	return token
}

// spend follows one launch link and returns what the gateway answered.
func spend(t *testing.T, controller *LaunchController, ticket string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/launch?"+localauth.TicketParam+"="+ticket, nil)
	controller.Launch(recorder, request)
	return recorder
}

// sessionCookie returns the credential cookie a response set, or "".
func sessionCookie(response *http.Response) string {
	for _, cookie := range response.Cookies() {
		if cookie.Name == localauth.SessionCookie && cookie.MaxAge >= 0 {
			return cookie.Value
		}
	}
	return ""
}

func TestALaunchLinkOpensASessionOnce(t *testing.T) {
	token := launchToken(t, 2)
	controller := NewLaunchController(localauth.NewGate(token, "127.0.0.1"))
	ticket, err := localauth.MintTicket(token, time.Now())
	require.NoError(t, err)

	first := spend(t, controller, ticket).Result()
	defer first.Body.Close()
	assert.Equal(t, http.StatusSeeOther, first.StatusCode)
	assert.Equal(t, "/", first.Header.Get("Location"))
	assert.NotEmpty(t, sessionCookie(first), "the first use should hand over a session")

	// The same link again. A launch URL survives in shell history, in a
	// clipboard, and in whatever the operator pasted it into, so the second use
	// is the one an attacker gets.
	second := spend(t, controller, ticket).Result()
	defer second.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, second.StatusCode)
	assert.Empty(t, sessionCookie(second))
}

func TestAnExpiredLaunchLinkOpensNothing(t *testing.T) {
	token := launchToken(t, 2)
	gate := localauth.NewGate(token, "127.0.0.1")
	minted := time.Now()
	controller := &LaunchController{
		gate: gate,
		now:  func() time.Time { return minted.Add(localauth.TicketTTL + time.Second) },
	}
	ticket, err := localauth.MintTicket(token, minted)
	require.NoError(t, err)

	response := spend(t, controller, ticket).Result()
	defer response.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Empty(t, sessionCookie(response))
}

func TestALaunchLinkFromAnotherTokenOpensNothing(t *testing.T) {
	// Rotation is revocation: a link minted before `starport auth rotate` has
	// to stop working, and it is the same code path as an outright forgery.
	controller := NewLaunchController(localauth.NewGate(launchToken(t, 3), "127.0.0.1"))
	ticket, err := localauth.MintTicket(launchToken(t, 2), time.Now())
	require.NoError(t, err)

	response := spend(t, controller, ticket).Result()
	defer response.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Empty(t, sessionCookie(response))
}

func TestARefusalClearsAStaleSession(t *testing.T) {
	controller := NewLaunchController(localauth.NewGate(launchToken(t, 2), "127.0.0.1"))

	response := spend(t, controller, "not-a-ticket").Result()
	defer response.Body.Close()

	var cleared []string
	for _, cookie := range response.Cookies() {
		if cookie.MaxAge < 0 {
			cleared = append(cleared, cookie.Name)
		}
	}
	assert.ElementsMatch(t,
		[]string{localauth.SessionCookie, localauth.SessionMarkerCookie},
		cleared,
		"a refused launch must not leave the console claiming to be signed in",
	)
}

func TestARefusalNamesTheWayBackInAndNothingElse(t *testing.T) {
	controller := NewLaunchController(localauth.NewGate(launchToken(t, 2), "127.0.0.1"))
	ticket, err := localauth.MintTicket(launchToken(t, 9), time.Now())
	require.NoError(t, err)

	recorder := spend(t, controller, ticket)
	body := recorder.Body.String()
	assert.Contains(t, body, "starport ui")
	// The ticket a caller presented is a credential-shaped value, and echoing
	// it into a page puts it somewhere a browser will keep.
	assert.NotContains(t, body, ticket)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
}

func TestAGatewayWithNoLocalTokenRefusesEveryLaunch(t *testing.T) {
	// A nil gate is a gateway assembled without a local admin token. It must
	// refuse rather than panic, and it must not accept anything.
	controller := NewLaunchController(nil)
	ticket, err := localauth.MintTicket(launchToken(t, 2), time.Now())
	require.NoError(t, err)

	response := spend(t, controller, ticket).Result()
	defer response.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Empty(t, sessionCookie(response))
}
