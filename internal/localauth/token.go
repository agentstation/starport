// Package localauth owns the local operator credential.
//
// A gateway API key belongs to an account and travels: it is issued, stored in a
// browser, pasted into an SDK, and revoked. The local admin token belongs to
// nobody and does not travel. It is a file on the machine running the gateway,
// readable only by the account that runs it, and holding it is a claim about
// where you are rather than who you are.
//
// The two are kept apart deliberately. An operator who has just installed
// Starport has no key and no way to issue one, because issuing a key is itself
// an admin act. The local admin token is what breaks that circle: the machine
// vouches for the person sitting at it, and everything else follows from there.
package localauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/authmode"
)

const (
	// TokenVersion is the record layout this binary reads and writes. A record
	// from a newer binary is refused rather than guessed at, because a field
	// this version does not know about could be the one carrying a restriction.
	TokenVersion = 1

	// TokenScope names what the token authorizes. It is stored in the record so
	// a future token with a narrower scope cannot be read as this one.
	TokenScope = "local-admin"

	// TokenPrefix marks the secret as a local admin token at a glance. Gateway
	// API keys begin "STARPORT_", so a lowercase prefix means neither can be
	// mistaken for the other in a terminal, a log line, or a support thread.
	TokenPrefix = "starport_local_"

	// RotateCommand names the command that clears the exposure refusal. The
	// startup message and the CLI status line both point at it, and a refusal
	// that named a command spelled differently from the one that fixes it would
	// send an operator looking for a command that does not exist.
	RotateCommand = "starport auth rotate"

	secretBytes = 32
)

// ErrCorruptRecord reports a token file this binary cannot read as a record.
var ErrCorruptRecord = errors.New("the local admin token file is not a token record")

// ErrUnsupportedVersion reports a record written by a different binary.
var ErrUnsupportedVersion = errors.New("the local admin token file uses an unsupported version")

// Token is one local admin credential and everything an operator needs to
// judge it.
type Token struct {
	// Version is the record layout. It is first so a hand-read file leads with
	// the field that decides whether the rest means anything.
	Version int `json:"version"`
	// Secret is the value a caller presents.
	Secret string `json:"secret"`
	// Generation counts how many times this machine has minted a token. It is
	// what an operator compares after a rotation to see that it took.
	Generation uint64 `json:"generation"`
	// IssuedAt is when this secret was minted.
	IssuedAt time.Time `json:"issued_at"`
	// Scope names what the token authorizes.
	Scope string `json:"scope"`
	// RotatedAt is when an operator last replaced the secret deliberately. A
	// nil value means never: the token is still the one first boot minted.
	RotatedAt *time.Time `json:"rotated_at,omitempty"`
}

// Authorizes reports whether candidate is this token. The comparison is
// constant time, because the alternative leaks the secret one byte at a time to
// anyone who can time the answer.
func (t Token) Authorizes(candidate string) bool {
	if t.Secret == "" || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(t.Secret), []byte(candidate)) == 1
}

// Rotated reports whether an operator has replaced the first-boot secret.
//
// A never-rotated token is the one this machine printed when it first started.
// That value has been in a terminal, and a terminal is scrollback, a tmux
// buffer, a screen share, and a CI log. It is safe where it was born and
// nowhere else.
func (t Token) Rotated() bool {
	return t.RotatedAt != nil && !t.RotatedAt.IsZero()
}

// Validate reports whether a record read from disk is one this binary may act
// on.
func (t Token) Validate() error {
	if t.Version != TokenVersion {
		return fmt.Errorf("%w: found %d, expected %d", ErrUnsupportedVersion, t.Version, TokenVersion)
	}
	if t.Scope != TokenScope {
		return fmt.Errorf("%w: scope %q is not %q", ErrCorruptRecord, t.Scope, TokenScope)
	}
	if t.Generation == 0 {
		return fmt.Errorf("%w: generation must be greater than zero", ErrCorruptRecord)
	}
	if !strings.HasPrefix(t.Secret, TokenPrefix) || len(t.Secret) <= len(TokenPrefix) {
		return fmt.Errorf("%w: the secret is not a local admin token", ErrCorruptRecord)
	}
	if t.IssuedAt.IsZero() {
		return fmt.Errorf("%w: the issue time is missing", ErrCorruptRecord)
	}
	return nil
}

// Redacted is the token with its secret removed, for anything that reports on
// the token rather than presents it.
func (t Token) Redacted() Token {
	t.Secret = ""
	return t
}

// Mint creates a new token at the given generation. The caller decides the
// generation, because only the caller knows whether this is a first boot or a
// rotation of something already on disk.
func Mint(generation uint64, now time.Time) (Token, error) {
	if generation == 0 {
		return Token{}, fmt.Errorf("%w: generation must be greater than zero", ErrCorruptRecord)
	}
	buffer := make([]byte, secretBytes)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return Token{}, fmt.Errorf("read random bytes for a local admin token: %w", err)
	}
	return Token{
		Version:    TokenVersion,
		Secret:     TokenPrefix + base64.RawURLEncoding.EncodeToString(buffer),
		Generation: generation,
		IssuedAt:   now.UTC(),
		Scope:      TokenScope,
	}, nil
}

// AllowsExposure reports whether a gateway bound to bindHost may serve this
// token.
//
// It is the same shape as the AON6 authentication tripwire and reuses its
// loopback rule rather than restating it: on a loopback address the only
// callers are already on this machine, and holding the token proves nothing
// they could not do anyway. On an address the network can reach, the token
// becomes a credential, and a first-boot secret that has been sitting in a
// terminal is not one.
//
// The way out is a rotation, not an acknowledgment flag. An operator who
// acknowledges the risk still has the compromised value; an operator who
// rotates has a secret that was never printed at boot.
func AllowsExposure(bindHost string, token Token) bool {
	return authmode.LoopbackHost(bindHost) || token.Rotated()
}
