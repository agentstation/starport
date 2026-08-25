package localauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	// SessionCookie carries the console session. It is HttpOnly, so the console
	// never reads it: the gateway is the only party that needs the value, and a
	// credential a script can read is a credential a script can send somewhere.
	SessionCookie = "starport_session"

	// SessionMarkerCookie tells the console that a session exists. It carries
	// no secret and authenticates nothing — the gateway ignores it entirely.
	// It exists so the console can render a signed-in shell without a request
	// whose only purpose is to ask whether the next request would work.
	SessionMarkerCookie = "starport_session_present"

	// SessionTTL is how long one launch lasts. It is a working day rather than
	// a month: the way back is `starport ui`, which costs a command and no
	// credential handling, so a short life costs an operator almost nothing and
	// bounds how long a borrowed laptop stays signed in.
	SessionTTL = 12 * time.Hour

	sessionPurpose = "starport.console-session.v1"
)

var (
	// ErrSessionExpired reports a session this gateway signed but will no
	// longer honour.
	ErrSessionExpired = errors.New("the console session has expired")
	// ErrSessionMalformed reports a correctly signed session whose payload this
	// version cannot read.
	ErrSessionMalformed = errors.New("the console session is not a session record")
)

// Session is a browser that redeemed a launch ticket on this machine.
//
// It names no account for the same reason a ticket does not: the claim is
// "this browser proved it could read the local admin token file", and the
// identity that claim maps to is the gateway's decision, not the cookie's.
type Session struct {
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// sessionPayload is the signed body of the cookie.
type sessionPayload struct {
	// IssuedAt and ExpiresAt are unix milliseconds. Both are signed, so a
	// gateway keeps no session table and a browser cannot extend its own stay.
	IssuedAt  int64 `json:"i"`
	ExpiresAt int64 `json:"e"`
}

// IssueSession mints the cookie value for a browser that redeemed a ticket.
func IssueSession(token Token, now time.Time) (string, Session, error) {
	if err := token.Validate(); err != nil {
		return "", Session{}, err
	}
	session := Session{IssuedAt: now.UTC(), ExpiresAt: now.Add(SessionTTL).UTC()}
	payload, err := json.Marshal(sessionPayload{
		IssuedAt:  session.IssuedAt.UnixMilli(),
		ExpiresAt: session.ExpiresAt.UnixMilli(),
	})
	if err != nil {
		return "", Session{}, fmt.Errorf("encode a console session: %w", err)
	}
	return sign(token, sessionPurpose, payload), session, nil
}

// VerifySession reports the session a cookie value stands for.
//
// A rotation of the local admin token changes the signing key, so every cookie
// issued under the old secret fails here with ErrBadSignature. That is the
// whole revocation mechanism: there is no session list to clear, and no window
// in which a gateway holding the new token still honours the old sessions.
func VerifySession(raw string, token Token, now time.Time) (Session, error) {
	payload, err := unsign(token, sessionPurpose, raw)
	if err != nil {
		return Session{}, err
	}
	var record sessionPayload
	if err := json.Unmarshal(payload, &record); err != nil {
		return Session{}, fmt.Errorf("%w: %w", ErrSessionMalformed, err)
	}
	if record.IssuedAt == 0 || record.ExpiresAt == 0 {
		return Session{}, ErrSessionMalformed
	}
	if !now.Before(time.UnixMilli(record.ExpiresAt)) {
		return Session{}, ErrSessionExpired
	}
	return Session{
		IssuedAt:  time.UnixMilli(record.IssuedAt).UTC(),
		ExpiresAt: time.UnixMilli(record.ExpiresAt).UTC(),
	}, nil
}

// SessionCookies returns the pair a successful launch sets: the credential and
// the marker the console reads.
//
// SameSite is Lax rather than Strict. The console is reached by following a
// link the CLI printed, and Strict withholds the cookie on exactly that
// navigation, so an operator would land on the console signed out and the
// launch would appear to have failed.
//
// Secure is set from the scheme of the request that redeemed the ticket rather
// than assumed. A gateway behind TLS should mark the cookie Secure; the
// loopback console is plain HTTP, and a Secure cookie there is one a browser
// silently discards.
func SessionCookies(value string, session Session, secure bool) []*http.Cookie {
	return []*http.Cookie{
		//nolint:gosec // G124 wants Secure unconditionally. The default deployment
		// is loopback HTTP, where a Secure cookie is one the browser discards, so
		// the flag follows the scheme that redeemed the ticket.
		{
			Name:     SessionCookie,
			Value:    value,
			Path:     "/",
			Expires:  session.ExpiresAt,
			MaxAge:   int(time.Until(session.ExpiresAt).Seconds()),
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		},
		//nolint:gosec // G124 wants HttpOnly. The marker is deliberately readable:
		// it carries no authority, and the console needs it to know not to prompt
		// for a key. Forging it yields a rendered page and a gateway that refuses
		// every call. The credential above stays HttpOnly.
		{
			Name:  SessionMarkerCookie,
			Value: "1",
			Path:  "/",
			// The marker expires with the session it describes, so a console
			// that reads it is never told it is signed in by a cookie that
			// outlived the credential.
			Expires:  session.ExpiresAt,
			MaxAge:   int(time.Until(session.ExpiresAt).Seconds()),
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		},
	}
}

// ClearedSessionCookies expire both cookies. The gateway sends them when it
// refuses a session cookie it once issued, so a browser holding a cookie from
// a rotated token stops presenting it and the console stops claiming to be
// signed in.
func ClearedSessionCookies(secure bool) []*http.Cookie {
	expire := func(name string, httpOnly bool) *http.Cookie {
		//nolint:gosec // G124 reads the attributes of a cookie being expired. The
		// value is empty and Max-Age is negative; the attributes only have to
		// match the cookie being replaced for a browser to drop it.
		return &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
			HttpOnly: httpOnly,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		}
	}
	return []*http.Cookie{
		expire(SessionCookie, true),
		expire(SessionMarkerCookie, false),
	}
}
