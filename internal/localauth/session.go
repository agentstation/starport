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

// Session is a browser a grant admitted on this machine.
//
// It names no account for the same reason a ticket does not: the two grants
// that ship both claim "this browser proved it could reach something only a
// process on this machine could hand it", and the identity that claim maps to
// is the gateway's decision, not the cookie's. An identity grant would be the
// one that changes that, which is why the session records which grant minted
// it rather than treating them all as the same admission.
type Session struct {
	IssuedAt  time.Time
	ExpiresAt time.Time
	// Grant is the kind that minted this session. It is signed with the rest,
	// so a browser cannot relabel its own admission.
	Grant GrantKind
	// Subject is who an identity provider said the caller is, and is empty for
	// every other grant. The pairing is exact in both directions and enforced
	// at issue and at verify: a grant that claims to know who you are has to
	// say who, and a grant that only knows where you are may not claim more.
	Subject string
}

// sessionPayload is the signed body of the cookie.
type sessionPayload struct {
	// IssuedAt and ExpiresAt are unix milliseconds. Both are signed, so a
	// gateway keeps no session table and a browser cannot extend its own stay.
	IssuedAt  int64 `json:"i"`
	ExpiresAt int64 `json:"e"`
	// Grant is the kind that minted the session.
	Grant GrantKind `json:"g"`
	// Subject is omitted entirely for the machine-local grants rather than
	// written empty, so their cookies do not carry a field that means nothing
	// for them.
	Subject string `json:"s,omitempty"`
}

// IssueSession mints the cookie value for a browser one grant admitted.
//
// It is unexported behavior in every sense that matters: the grants in this
// package are its only callers, and a caller that reached it directly would be
// minting a session no grant vouched for.
func IssueSession(token Token, grant GrantKind, now time.Time) (string, Session, error) {
	if grant == GrantIdentity {
		// The identity grant carries a subject, and this entry point has no way
		// to supply one. Refusing here is what makes the pairing structural
		// rather than a rule every future caller has to remember.
		return "", Session{}, ErrIdentitySubjectMissing
	}
	return issueSession(token, grant, "", now)
}

// issueIdentitySession mints a session for a person an identity provider named.
// It is unexported because the identity grant is its only legitimate caller:
// anything else reaching it would be asserting an identity no provider vouched
// for.
func issueIdentitySession(token Token, subject string, now time.Time) (string, Session, error) {
	if subject == "" {
		return "", Session{}, ErrIdentitySubjectMissing
	}
	return issueSession(token, GrantIdentity, subject, now)
}

func issueSession(
	token Token, grant GrantKind, subject string, now time.Time,
) (string, Session, error) {
	if err := token.Validate(); err != nil {
		return "", Session{}, err
	}
	if !knownGrantKind(grant) {
		return "", Session{}, fmt.Errorf("%w: %s", ErrGrantUnknown, grant)
	}
	session := Session{
		IssuedAt:  now.UTC(),
		ExpiresAt: now.Add(SessionTTL).UTC(),
		Grant:     grant,
		Subject:   subject,
	}
	payload, err := json.Marshal(sessionPayload{
		IssuedAt:  session.IssuedAt.UnixMilli(),
		ExpiresAt: session.ExpiresAt.UnixMilli(),
		Grant:     session.Grant,
		Subject:   session.Subject,
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
	// A grant name this binary does not register is refused rather than
	// honoured. The signature only proves this machine's token minted the
	// value; it says nothing about whether this version understands what the
	// grant was allowed to do, and the safe reading of an unknown grant is not
	// "the same as the ones I know".
	if !knownGrantKind(record.Grant) {
		return Session{}, fmt.Errorf("%w: %w: %s", ErrSessionMalformed, ErrGrantUnknown, record.Grant)
	}
	// The subject and the grant have to agree. A machine-local cookie carrying a
	// subject would let a grant that only proves reach assert an identity, and an
	// identity cookie without one names nobody. The subject is not echoed into
	// the error: it identifies a person, and a refusal is a thing that gets
	// logged.
	if (record.Grant == GrantIdentity) != (record.Subject != "") {
		return Session{}, fmt.Errorf(
			"%w: the %s grant does not carry the subject it was issued with",
			ErrSessionMalformed, record.Grant,
		)
	}
	if !now.Before(time.UnixMilli(record.ExpiresAt)) {
		return Session{}, ErrSessionExpired
	}
	return Session{
		IssuedAt:  time.UnixMilli(record.IssuedAt).UTC(),
		ExpiresAt: time.UnixMilli(record.ExpiresAt).UTC(),
		Grant:     record.Grant,
		Subject:   record.Subject,
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
		// #nosec G124 -- the rule wants Secure unconditionally. The default
		// deployment is loopback HTTP, where a Secure cookie is one the browser
		// discards, so the flag follows the scheme that redeemed the ticket.
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
		// #nosec G124 -- the rule wants HttpOnly. The marker is deliberately
		// readable: it carries no authority, and the console needs it to know not
		// to prompt for a key. Forging it yields a rendered page and a gateway
		// that refuses every call. The credential above stays HttpOnly.
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
		// #nosec G124 -- the rule reads the attributes of a cookie being expired.
		// The value is empty and Max-Age is negative; the attributes only have to
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
