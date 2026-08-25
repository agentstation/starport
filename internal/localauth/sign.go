package localauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ErrBadSignature reports a value this machine's local admin token did not
// sign. It is the answer to a forged value and to a value signed by a token
// that has since been rotated, and those two cases are deliberately one error:
// telling a caller which it was would say whether their guess had the right
// shape.
var ErrBadSignature = errors.New("the value was not signed by this machine's local admin token")

// signingKey derives a key for one purpose from the token secret.
//
// The derivation is what makes rotation a revocation. Every browser-facing
// value in this package is signed with a key that exists only as a function of
// the current secret, so replacing the secret invalidates every outstanding
// ticket and session at once, with nothing to enumerate and nothing to expire.
//
// The purpose string separates the uses. Without it a value minted for one
// purpose would verify as the other, and a launch ticket is short-lived and
// single-use precisely because a session is neither.
func signingKey(token Token, purpose string) []byte {
	mac := hmac.New(sha256.New, []byte(token.Secret))
	mac.Write([]byte(purpose))
	return mac.Sum(nil)
}

// sign returns "<payload>.<mac>", both base64url without padding.
func sign(token Token, purpose string, payload []byte) string {
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, signingKey(token, purpose))
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// unsign returns the payload of a value this token signed for this purpose.
//
// Nothing is decoded from the payload before the signature is checked. A
// caller that read the payload first would be acting on bytes an attacker
// chose, and every field in it describes a permission or a lifetime.
func unsign(token Token, purpose string, raw string) ([]byte, error) {
	encoded, signature, found := strings.Cut(raw, ".")
	if !found {
		return nil, ErrBadSignature
	}
	presented, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, ErrBadSignature
	}
	mac := hmac.New(sha256.New, signingKey(token, purpose))
	mac.Write([]byte(encoded))
	if !hmac.Equal(presented, mac.Sum(nil)) {
		return nil, ErrBadSignature
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: the payload is not base64url", ErrBadSignature)
	}
	return payload, nil
}
