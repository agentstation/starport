package localauth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestThePastedTokenAdmitsTheOperator is the asymmetry this campaign exists to
// close: `starport auth token` prints a value, and until now nothing accepted
// it. A session it mints has to be a real session, and it has to say which
// grant admitted it.
func TestThePastedTokenAdmitsTheOperator(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token, "127.0.0.1")
	now := time.Now()

	cookie, session, err := gate.MintSession(
		GrantLocalToken, GrantRequest{Claim: token.Secret}, now,
	)
	require.NoError(t, err)
	require.Equal(t, GrantLocalToken, session.Grant)

	verified, err := gate.Verify(cookie, now)
	require.NoError(t, err)
	require.Equal(t, GrantLocalToken, verified.Grant)
}

// TestAWrongValueGetsOneAnswer holds the property ErrBadSignature already has
// for tickets. A refusal that separated "wrong secret" from "right shape,
// wrong bytes" would tell a caller whether their guess was getting warmer.
func TestAWrongValueGetsOneAnswer(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token, "127.0.0.1")

	for name, candidate := range map[string]string{
		"an empty value":                     "",
		"whitespace":                         "   ",
		"a value with the right prefix":      TokenPrefix + "not-the-secret",
		"a value with no prefix at all":      "hunter2",
		"this machine's token, one byte off": token.Secret[:len(token.Secret)-1] + "x",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := gate.MintSession(
				GrantLocalToken, GrantRequest{Claim: candidate}, time.Now(),
			)
			require.ErrorIs(t, err, ErrTokenRejected)
		})
	}
}

// TestAGatewayAPIKeyIsNamedRatherThanRefused is the one deliberate exception
// to the single-refusal rule. A reader who pasted a STARPORT_ key made a
// category error, not a guess: naming it narrows no search space, because the
// prefix is public, and leaving them to work out which of two credentials the
// field wants is exactly the confusion this campaign is here to remove.
func TestAGatewayAPIKeyIsNamedRatherThanRefused(t *testing.T) {
	gate := NewGate(testToken(t, 1), "127.0.0.1")

	_, _, err := gate.MintSession(
		GrantLocalToken,
		GrantRequest{Claim: GatewayAPIKeyPrefix + "abc123"},
		time.Now(),
	)
	require.ErrorIs(t, err, ErrGatewayAPIKeyPresented)
	require.NotErrorIs(t, err, ErrTokenRejected)
}

// TestAnUnrotatedTokenIsRefusedFromOffMachine covers the hole the startup
// check cannot see.
//
// internal/app refuses to start when AllowsExposure is false for the
// configured bind host. A reverse proxy in front of a loopback gateway
// satisfies that check — the process really did bind 127.0.0.1 — while making
// this route reachable from the network. A first-boot secret has been printed
// to a terminal, so accepting it from off-machine would hand the console to
// anyone who read that scrollback.
//
// Rotating clears it, because a rotated secret was never printed at boot. That
// is the same way out AllowsExposure already offers, and the test asserts both
// halves so the refusal cannot quietly become permanent.
func TestAnUnrotatedTokenIsRefusedFromOffMachine(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token, "127.0.0.1")
	remote := GrantRequest{Claim: token.Secret, RemoteHost: "203.0.113.7"}

	_, _, err := gate.MintSession(GrantLocalToken, remote, time.Now())
	require.ErrorIs(t, err, ErrRemoteTokenRefused)

	// The same secret from this machine is fine.
	_, _, err = gate.MintSession(
		GrantLocalToken, GrantRequest{Claim: token.Secret, RemoteHost: "127.0.0.1"}, time.Now(),
	)
	require.NoError(t, err)

	rotatedAt := time.Now()
	rotated := token
	rotated.RotatedAt = &rotatedAt
	rotatedGate := NewGate(rotated, "0.0.0.0")

	_, session, err := rotatedGate.MintSession(
		GrantLocalToken, GrantRequest{Claim: rotated.Secret, RemoteHost: "203.0.113.7"}, time.Now(),
	)
	require.NoError(t, err)
	require.Equal(t, GrantLocalToken, session.Grant)
}

// TestFailedAttemptsAreSerialized checks the shape of the throttle rather than
// its duration. A per-caller delay is one an attacker sidesteps by opening
// more connections, so the delay has to be under a lock that every rejected
// attempt takes — and a successful paste must never wait behind it, or an
// operator pays for someone else's guessing.
func TestFailedAttemptsAreSerialized(t *testing.T) {
	token := testToken(t, 1)
	var (
		mu       sync.Mutex
		overlap  bool
		inFlight int
		slept    atomic.Int64
	)
	grant := &localTokenGrant{
		token:    token,
		bindHost: "127.0.0.1",
		delay:    time.Millisecond,
		sleep: func(d time.Duration) {
			slept.Add(1)
			mu.Lock()
			inFlight++
			if inFlight > 1 {
				overlap = true
			}
			mu.Unlock()
			time.Sleep(d)
			mu.Lock()
			inFlight--
			mu.Unlock()
		},
	}

	// The refusals are collected rather than asserted in the goroutines:
	// require fails by calling runtime.Goexit, which off the test goroutine
	// hangs the run instead of failing it.
	const attempts = 8
	refusals := make([]error, attempts)
	var wait sync.WaitGroup
	for i := range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, refusals[i] = grant.Mint(GrantRequest{Claim: "wrong"}, time.Now())
		}()
	}
	wait.Wait()
	for _, err := range refusals {
		require.ErrorIs(t, err, ErrTokenRejected)
	}

	require.Equal(t, int64(attempts), slept.Load(), "every rejected attempt pays the delay")
	require.False(t, overlap, "rejected attempts must not run the delay concurrently")

	// A correct paste does not touch the throttle.
	before := slept.Load()
	_, _, err := grant.Mint(GrantRequest{Claim: token.Secret}, time.Now())
	require.NoError(t, err)
	require.Equal(t, before, slept.Load(), "a successful paste never waits")
}
