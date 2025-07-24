// Package apikeys provides API key management and validation for the Starport gateway.
package apikeys

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// APIKey represents an API key for authenticating with the gateway
type APIKey struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Hash            string                 `json:"hash"`
	Scopes          []string               `json:"scopes"`
	AllowedModels   []string               `json:"allowed_models,omitempty"`
	RateLimitConfig map[string]interface{} `json:"rate_limit_config,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	Active          bool                   `json:"active"`
	CreatedAt       time.Time              `json:"created_at"`
	ExpiresAt       *time.Time             `json:"expires_at,omitempty"`
}

// Common validation errors
var (
	ErrMissingID         = errors.New("missing id")
	ErrMissingHash       = errors.New("missing hash")
	ErrMissingScopes     = errors.New("missing scopes")
	ErrInvalidName       = errors.New("invalid name: must be 1-255 characters")
	ErrInvalidScope      = errors.New("invalid scope: must be non-empty")
	ErrInvalidModel      = errors.New("invalid model: must be non-empty")
	ErrInvalidExpiration = errors.New("expires_at must be after created_at")
)

// validNameRegex ensures names are alphanumeric with hyphens, underscores
var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Validate validates the APIKey fields
func (k *APIKey) Validate() error {
	if k.ID == "" {
		return ErrMissingID
	}
	if k.Name == "" || len(k.Name) > 255 {
		return ErrInvalidName
	}
	if !validNameRegex.MatchString(k.Name) {
		return fmt.Errorf("%w: must contain only alphanumeric characters, hyphens, and underscores", ErrInvalidName)
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
	return nil
}

// IsExpired checks if the API key has expired
func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// HasScope checks if the API key has the given scope
func (k *APIKey) HasScope(scope string) bool {
	if scope == "" {
		return false
	}
	for _, s := range k.Scopes {
		if s == scope || s == "*" {
			return true
		}
	}
	return false
}

// CanUseModel checks if the API key can use the given model
func (k *APIKey) CanUseModel(model string) bool {
	if len(k.AllowedModels) == 0 {
		return true // No restrictions
	}
	for _, m := range k.AllowedModels {
		if m == model || m == "*" {
			return true
		}
	}
	return false
}
