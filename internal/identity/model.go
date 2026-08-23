// Package identity owns gateway API-key identity and persistence.
package identity

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/agentstation/starport/internal/limits"
)

// APIKey is one gateway authentication identity.
type APIKey struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Hash          string         `json:"hash"`
	Scopes        []string       `json:"scopes"`
	AllowedModels []string       `json:"allowed_models,omitempty"`
	Limits        *limits.Limits        `json:"limits,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Active        bool           `json:"active"`
	CreatedAt     time.Time      `json:"created_at"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
}

var (
	// ErrMissingID reports an identity without a durable ID.
	ErrMissingID = errors.New("missing id")
	// ErrMissingHash reports an identity without a key hash.
	ErrMissingHash = errors.New("missing hash")
	// ErrMissingScopes reports an identity without any granted scope.
	ErrMissingScopes = errors.New("missing scopes")
	// ErrInvalidName reports an invalid identity name.
	ErrInvalidName = errors.New("invalid name: must be 1-255 characters")
	// ErrInvalidScope reports an empty identity scope.
	ErrInvalidScope = errors.New("invalid scope: must be non-empty")
	// ErrInvalidModel reports an empty allowed-model entry.
	ErrInvalidModel = errors.New("invalid model: must be non-empty")
	// ErrInvalidExpiration reports an expiration before identity creation.
	ErrInvalidExpiration = errors.New("expires_at must be after created_at")
)

var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateName checks the public identity-name contract.
func ValidateName(name string) error {
	if name == "" || len(name) > 255 {
		return ErrInvalidName
	}
	if !validNameRegex.MatchString(name) {
		return fmt.Errorf("%w: must contain only alphanumeric characters, hyphens, and underscores", ErrInvalidName)
	}
	return nil
}

// Validate checks the API-key invariants.
func (k APIKey) Validate() error {
	if k.ID == "" {
		return ErrMissingID
	}
	if err := ValidateName(k.Name); err != nil {
		return err
	}
	if k.Hash == "" {
		return ErrMissingHash
	}
	if len(k.Scopes) == 0 {
		return ErrMissingScopes
	}
	for _, scope := range k.Scopes {
		if scope == "" {
			return ErrInvalidScope
		}
	}
	for _, model := range k.AllowedModels {
		if model == "" {
			return ErrInvalidModel
		}
	}
	if k.ExpiresAt != nil && k.ExpiresAt.Before(k.CreatedAt) {
		return ErrInvalidExpiration
	}
	if err := k.Limits.Validate(); err != nil {
		return err
	}
	return nil
}

// IsExpiredAt reports whether the identity expired at the supplied time.
func (k APIKey) IsExpiredAt(now time.Time) bool {
	return k.ExpiresAt != nil && now.After(*k.ExpiresAt)
}

// IsExpired reports whether the identity is expired now.
func (k APIKey) IsExpired() bool {
	return k.IsExpiredAt(time.Now())
}

// HasScope reports whether the identity grants a scope.
func (k APIKey) HasScope(scope string) bool {
	if scope == "" {
		return false
	}
	for _, candidate := range k.Scopes {
		if candidate == scope || candidate == "*" {
			return true
		}
	}
	return false
}

// CanUseModel reports whether the identity can use a model.
func (k APIKey) CanUseModel(model string) bool {
	if len(k.AllowedModels) == 0 {
		return true
	}
	for _, candidate := range k.AllowedModels {
		if candidate == model || candidate == "*" {
			return true
		}
	}
	return false
}
