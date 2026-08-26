package localauth

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestATicketMintedSessionRecordsItsGrant holds the property the rest of the
// campaign leans on: the kind that admitted a browser survives the round trip
// through the signed cookie. Without it, a log line and a later restriction
// would both have to guess, and every grant would look like every other.
func TestATicketMintedSessionRecordsItsGrant(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token)
	now := time.Now()

	ticket, err := gate.MintTicket(now)
	require.NoError(t, err)

	cookie, minted, err := gate.Redeem(ticket, now)
	require.NoError(t, err)
	require.Equal(t, GrantTicket, minted.Grant)

	verified, err := gate.Verify(cookie, now)
	require.NoError(t, err)
	require.Equal(t, GrantTicket, verified.Grant)
}

// TestASessionNamingAnUnknownGrantIsRefused is the reason the grant is signed
// rather than inferred. A correct signature proves only that this machine's
// token minted the value; it says nothing about what that version of the
// binary let the grant do. A newer Starport could add a grant with a narrower
// reach, and an older one that read the cookie as "some grant, close enough"
// would hand that browser the wide console.
//
// The missing-grant case covers the same hole from the other side: a payload
// written before the field existed must not fall through to a zero value that
// happens to compare equal to nothing.
func TestASessionNamingAnUnknownGrantIsRefused(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token)
	now := time.Now()

	forge := func(t *testing.T, grant GrantKind) string {
		t.Helper()
		payload, err := json.Marshal(sessionPayload{
			IssuedAt:  now.UnixMilli(),
			ExpiresAt: now.Add(SessionTTL).UnixMilli(),
			Grant:     grant,
		})
		require.NoError(t, err)
		return sign(token, sessionPurpose, payload)
	}

	for name, grant := range map[string]GrantKind{
		"a grant a newer binary added": GrantKind("delegated"),
		"no grant at all":              GrantKind(""),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := gate.Verify(forge(t, grant), now)
			require.ErrorIs(t, err, ErrSessionMalformed)
			require.ErrorIs(t, err, ErrGrantUnknown)
		})
	}
}

// TestAnUnregisteredGrantRefuses covers the shape that makes the identity
// grant shippable while inert: a caller asks for a grant by name and gets an
// error, so a kind this deployment does not offer is a value the gate returns
// rather than a nil dereference in whatever asked for it.
func TestAnUnregisteredGrantRefuses(t *testing.T) {
	gate := NewGate(testToken(t, 1))

	_, err := gate.Grant(GrantKind("saml"))
	require.ErrorIs(t, err, ErrGrantUnknown)

	_, _, err = gate.MintSession(GrantKind("saml"), GrantRequest{Claim: "anything"}, time.Now())
	require.ErrorIs(t, err, ErrGrantUnknown)
}

// TestAGrantIsFiledUnderTheKindItClaims guards the one way the registry can
// lie. A grant filed under a name it does not report would put one kind in the
// session and another in the log, and the mismatch would only surface in an
// incident.
func TestAGrantIsFiledUnderTheKindItClaims(t *testing.T) {
	gate := NewGate(testToken(t, 1))

	for kind, grant := range gate.grants {
		require.Equal(t, kind, grant.Kind())
	}
}
