// Package deployment owns the deployment identity and shared key prefix.
package deployment

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateID accepts an opaque deployment identity without normalization.
func ValidateID(id string) error {
	if id == "" || strings.TrimSpace(id) != id || len(id) > 256 || !utf8.ValidString(id) || strings.ContainsFunc(id, unicode.IsControl) {
		return errors.New("deployment ID requires at most 256 UTF-8 bytes without surrounding whitespace or control characters")
	}
	return nil
}

// KeyPrefix returns a versioned prefix with no delimiter or ACL glob ambiguity.
func KeyPrefix(id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	return "starport:v1:" + base64.RawURLEncoding.EncodeToString([]byte(id)) + ":", nil
}
