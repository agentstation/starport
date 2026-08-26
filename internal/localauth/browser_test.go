package localauth

import (
	"encoding/base64"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testToken(t *testing.T, generation uint64) Token {
	t.Helper()
	token, err := Mint(generation, time.Now())
	require.NoError(t, err)
	return token
}

// TestATicketWorksOnce is the property the whole ticket exists for. A launch
// link travels in shell history, a terminal buffer, and whatever the operator
// pasted it into, and every one of those is a place a second person can read it
// back.
func TestATicketWorksOnce(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token)
	now := time.Now()
	ticket, err := MintTicket(token, now)
	require.NoError(t, err)

	_, session, err := gate.Redeem(ticket, now)
	require.NoError(t, err)
	assert.True(t, session.ExpiresAt.After(now))

	_, _, err = gate.Redeem(ticket, now)
	assert.ErrorIs(t, err, ErrTicketUsed)
}

// TestConcurrentRedemptionsSpendATicketOnce covers the browser that opens a
// link twice, and the link opened at the same moment on two devices. Checking
// and recording a nonce is one decision, and a version that checked before it
// recorded would hand out two sessions for one ticket.
func TestConcurrentRedemptionsSpendATicketOnce(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token)
	now := time.Now()
	ticket, err := MintTicket(token, now)
	require.NoError(t, err)

	const attempts = 8
	var (
		wait      sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)
	wait.Add(attempts)
	for range attempts {
		go func() {
			defer wait.Done()
			if _, _, err := gate.Redeem(ticket, now); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wait.Wait()

	assert.Equal(t, 1, succeeded, "exactly one of %d concurrent redemptions may succeed", attempts)
}

func TestAnExpiredTicketIsRefused(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token)
	now := time.Now()
	ticket, err := MintTicket(token, now)
	require.NoError(t, err)

	_, _, err = gate.Redeem(ticket, now.Add(TicketTTL-time.Millisecond))
	require.NoError(t, err, "a ticket is good up to its expiry")

	fresh, err := MintTicket(token, now)
	require.NoError(t, err)
	_, _, err = gate.Redeem(fresh, now.Add(TicketTTL))
	assert.ErrorIs(t, err, ErrTicketExpired, "the expiry moment itself is too late")
}

// TestSpentTicketsAreForgottenOnceTheyExpire keeps the redemption set from
// being a memory leak that an operator can grow by running one command in a
// loop. Past its expiry a nonce is refused by the expiry check, so remembering
// it guards nothing.
func TestSpentTicketsAreForgottenOnceTheyExpire(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token)
	now := time.Now()
	for range 32 {
		ticket, err := MintTicket(token, now)
		require.NoError(t, err)
		_, _, err = gate.Redeem(ticket, now)
		require.NoError(t, err)
	}
	require.Len(t, gate.tickets.spent, 32)

	later := now.Add(TicketTTL)
	ticket, err := MintTicket(token, later)
	require.NoError(t, err)
	_, _, err = gate.Redeem(ticket, later)
	require.NoError(t, err)

	assert.Len(t, gate.tickets.spent, 1, "only the ticket that is still live is remembered")
}

// TestRotatingTheTokenRefusesOutstandingTickets is the revocation story for a
// link an operator printed and then thought better of. There is no ticket list
// to clear: the signing key is derived from the secret, so replacing the secret
// invalidates every outstanding ticket at once.
func TestRotatingTheTokenRefusesOutstandingTickets(t *testing.T) {
	first := testToken(t, 1)
	now := time.Now()
	ticket, err := MintTicket(first, now)
	require.NoError(t, err)

	rotated := NewGate(testToken(t, 2))
	_, _, err = rotated.Redeem(ticket, now)
	assert.ErrorIs(t, err, ErrBadSignature)
}

// TestATicketIsNotASession holds the two uses apart. Without a purpose in the
// key derivation, a ticket would verify as a session cookie: an operator would
// paste a launch link's ticket into a cookie and hold a twelve-hour credential
// minted from a ninety-second one.
func TestATicketIsNotASession(t *testing.T) {
	token := testToken(t, 1)
	now := time.Now()
	ticket, err := MintTicket(token, now)
	require.NoError(t, err)
	cookie, _, err := IssueSession(token, GrantTicket, now)
	require.NoError(t, err)

	_, err = VerifySession(ticket, token, now)
	assert.ErrorIs(t, err, ErrBadSignature, "a ticket must not authenticate as a session")

	gate := NewGate(token)
	_, _, err = gate.Redeem(cookie, now)
	assert.ErrorIs(t, err, ErrBadSignature, "a session must not spend as a ticket")
}

// TestTamperedValuesAreRefused checks the signature actually covers the
// payload. The expiry is the field an attacker would edit, and it is the one
// field a verifier reads before it can know whether to trust anything.
func TestTamperedValuesAreRefused(t *testing.T) {
	token := testToken(t, 1)
	now := time.Now()
	cookie, _, err := IssueSession(token, GrantTicket, now)
	require.NoError(t, err)
	payload, signature, found := strings.Cut(cookie, ".")
	require.True(t, found)

	for name, tampered := range map[string]string{
		"edited payload":   flipAByte(t, payload) + "." + signature,
		"edited signature": payload + "." + flipAByte(t, signature),
		"no signature":     payload,
		"empty":            "",
		"signature only":   "." + signature,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := VerifySession(tampered, token, now)
			assert.ErrorIs(t, err, ErrBadSignature)
		})
	}
}

// flipAByte edits what a base64url segment stands for, rather than the text of
// the segment. The two are not the same edit: a 32-byte MAC encodes to 43
// characters, and the last of them carries four bits of the MAC and two the
// encoding does not use. Rewriting that character can leave the decoded bytes
// identical, and the "tampered" value then verifies exactly as it should —
// which made the edited-signature case fail once every sixteen runs, on
// whichever platform drew the unlucky MAC. Flipping a decoded byte changes
// what was signed every time.
func flipAByte(t *testing.T, value string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(value)
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	raw[len(raw)-1] ^= 0xFF
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestAnExpiredSessionIsRefused(t *testing.T) {
	token := testToken(t, 1)
	now := time.Now()
	cookie, session, err := IssueSession(token, GrantTicket, now)
	require.NoError(t, err)
	assert.Equal(t, SessionTTL, session.ExpiresAt.Sub(session.IssuedAt))

	_, err = VerifySession(cookie, token, now.Add(SessionTTL-time.Second))
	require.NoError(t, err)

	_, err = VerifySession(cookie, token, now.Add(SessionTTL))
	assert.ErrorIs(t, err, ErrSessionExpired)
}

// TestRotatingTheTokenInvalidatesEverySession is the acceptance case for
// revoking console access. An operator who believes a laptop is compromised
// runs one command, and every browser holding a session stops being able to
// use it the moment the gateway restarts on the new token.
func TestRotatingTheTokenInvalidatesEverySession(t *testing.T) {
	first := testToken(t, 1)
	now := time.Now()
	cookie, _, err := IssueSession(first, GrantTicket, now)
	require.NoError(t, err)
	require.NotNil(t, cookie)

	_, err = VerifySession(cookie, testToken(t, 2), now)
	assert.ErrorIs(t, err, ErrBadSignature)
}

// TestSessionCookiesKeepTheSecretAwayFromScripts checks the pair the launch
// route sets. The credential is HttpOnly so no script can read it; the marker
// is readable and says nothing, which is what lets the console render a
// signed-in shell without holding a credential.
func TestSessionCookiesKeepTheSecretAwayFromScripts(t *testing.T) {
	token := testToken(t, 1)
	value, session, err := IssueSession(token, GrantTicket, time.Now())
	require.NoError(t, err)

	cookies := SessionCookies(value, session, false)
	require.Len(t, cookies, 2)

	credential, marker := cookies[0], cookies[1]
	assert.Equal(t, SessionCookie, credential.Name)
	assert.True(t, credential.HttpOnly, "the credential cookie must not be readable by scripts")
	assert.Equal(t, value, credential.Value)

	assert.Equal(t, SessionMarkerCookie, marker.Name)
	assert.False(t, marker.HttpOnly, "the marker exists to be read")
	assert.NotContains(t, marker.Value, value)
	assert.Equal(t, "1", marker.Value, "the marker carries no part of the credential")

	for _, cookie := range cookies {
		assert.False(t, cookie.Secure, "a plain-HTTP console gets cookies a browser will keep")
		assert.Equal(t, "/", cookie.Path)
	}
	for _, cookie := range SessionCookies(value, session, true) {
		assert.True(t, cookie.Secure, "a TLS console gets cookies a browser will only send over TLS")
	}
}

// TestClearedSessionCookiesExpireBoth matters because the marker is what the
// console believes. Clearing the credential and leaving the marker would give
// a console that reports itself signed in while every request behind it fails.
func TestClearedSessionCookiesExpireBoth(t *testing.T) {
	cleared := ClearedSessionCookies(false)
	require.Len(t, cleared, 2)
	names := map[string]bool{}
	for _, cookie := range cleared {
		names[cookie.Name] = true
		assert.Empty(t, cookie.Value)
		assert.Negative(t, cookie.MaxAge)
	}
	assert.True(t, names[SessionCookie])
	assert.True(t, names[SessionMarkerCookie])
}

// TestTicketPrefixIsTooShortToReplay guards the log line. A whole ticket in a
// log is a credential in a log for as long as the ticket lives, and logs are
// the one place a credential is copied by default.
func TestTicketPrefixIsTooShortToReplay(t *testing.T) {
	token := testToken(t, 1)
	now := time.Now()
	ticket, err := MintTicket(token, now)
	require.NoError(t, err)

	prefix := TicketPrefix(ticket)
	assert.Len(t, prefix, TicketLogPrefixLength)
	assert.True(t, strings.HasPrefix(ticket, prefix))

	gate := NewGate(token)
	_, _, err = gate.Redeem(prefix, now)
	assert.ErrorIs(t, err, ErrBadSignature)
}

func TestLaunchURLCarriesTheTicketAndNothingElse(t *testing.T) {
	token := testToken(t, 1)
	ticket, err := MintTicket(token, time.Now())
	require.NoError(t, err)

	raw, err := LaunchURL("http://127.0.0.1:8080", ticket)
	require.NoError(t, err)
	parsed, err := url.Parse(raw)
	require.NoError(t, err)

	assert.Equal(t, LaunchPath, parsed.Path)
	assert.Equal(t, ticket, parsed.Query().Get(TicketParam))
	assert.Len(t, parsed.Query(), 1)
	assert.NotContains(t, raw, token.Secret, "the token itself never travels in a URL")
}

// TestBrowsableBaseNamesAnAddressAPersonCanOpen covers the default production
// bind. A gateway on 0.0.0.0 has no address of its own, and a launch link
// pointing at one is a link an operator cannot use.
func TestBrowsableBaseNamesAnAddressAPersonCanOpen(t *testing.T) {
	for name, testCase := range map[string]struct {
		host   string
		port   int
		secure bool
		want   string
	}{
		"unspecified v4": {host: "0.0.0.0", port: 8080, want: "http://127.0.0.1:8080"},
		"unspecified v6": {host: "::", port: 8080, want: "http://[::1]:8080"},
		"empty":          {host: "", port: 8080, want: "http://127.0.0.1:8080"},
		"loopback":       {host: "127.0.0.1", port: 3000, want: "http://127.0.0.1:3000"},
		"bracketed v6":   {host: "[::1]", port: 3000, want: "http://[::1]:3000"},
		"named host":     {host: "gateway.internal", port: 443, want: "http://gateway.internal:443"},
		"tls":            {host: "127.0.0.1", port: 8443, secure: true, want: "https://127.0.0.1:8443"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, testCase.want, BrowsableBase(testCase.host, testCase.port, testCase.secure))
		})
	}
}

// TestAZeroGateRefusesRatherThanPanics covers composition that did not finish.
// A nil gate is what a build without a local token would hand the router, and
// the route stays mounted, so it has to answer rather than crash.
func TestAZeroGateRefusesRatherThanPanics(t *testing.T) {
	var gate *Gate
	_, _, err := gate.Redeem("anything", time.Now())
	assert.ErrorIs(t, err, ErrBadSignature)
	_, err = gate.Verify("anything", time.Now())
	assert.ErrorIs(t, err, ErrBadSignature)
	assert.Zero(t, gate.Generation())
}

// TestGateMintsTicketsItsOwnRedeemAccepts closes the loop for an in-process
// launch, where `starport dev` has the gateway and never touches the file.
func TestGateMintsTicketsItsOwnRedeemAccepts(t *testing.T) {
	gate := NewGate(testToken(t, 1))
	now := time.Now()

	ticket, err := gate.MintTicket(now)
	require.NoError(t, err)
	cookie, _, err := gate.Redeem(ticket, now)
	require.NoError(t, err)

	session, err := gate.Verify(cookie, now)
	require.NoError(t, err)
	assert.True(t, session.ExpiresAt.After(now))
}
