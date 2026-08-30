package events

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SignatureHeader carries the delivery's HMAC so a receiver can prove the
// body came from this gateway and arrived unchanged.
//
// #nosec G101 -- An HTTP header name, not a credential.
//
//nolint:gosec // An HTTP header name, not a credential.
const SignatureHeader = "X-Starport-Signature"

// signaturePrefix names the algorithm inside the header value, so a later
// algorithm can coexist with receivers verifying this one.
const signaturePrefix = "sha256="

// Sign computes the header value for one delivery body: "sha256=" and the
// lowercase hex HMAC-SHA256 of the body under the shared secret.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether header is the valid signature for body under
// secret. The comparison is constant-time. It is what a receiver runs, and
// the operator guide's sample encodes the same check.
func Verify(secret, body []byte, header string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(header))
}
