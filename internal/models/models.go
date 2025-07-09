// Package models provides data models for the Starport gateway.
package models

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

// Preset represents a reusable configuration template
type Preset struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Config      map[string]interface{} `json:"config"`
	Version     int                    `json:"version"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// BYOKCredential represents an encrypted credential for bring-your-own-key
type BYOKCredential struct {
	APIKeyID            string                 `json:"api_key_id"`
	Provider            string                 `json:"provider"`
	EncryptedCredential string                 `json:"encrypted_credential"`
	Config              map[string]interface{} `json:"config,omitempty"`     // Provider-specific config (endpoints, versions, etc)
	IsFallback          bool                   `json:"is_fallback"`          // Use as fallback when rate limited
	Priority            int                    `json:"priority"`             // Order preference (lower = higher priority)
	CreatedAt           time.Time              `json:"created_at"`
	LastUsed            *time.Time             `json:"last_used,omitempty"`
	UsageCount          int64                  `json:"usage_count"`
	UpdatedAt           time.Time              `json:"updated_at"`
}

// DefaultKey represents a gateway-wide default provider key
type DefaultKey struct {
	Provider            string                 `json:"provider"`
	EncryptedCredential string                 `json:"encrypted_credential"`
	Config              map[string]interface{} `json:"config,omitempty"`
	RateLimit           *RateLimitConfig       `json:"rate_limit,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
}

// RateLimitConfig defines rate limiting for a default key
type RateLimitConfig struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	TokensPerMinute   int `json:"tokens_per_minute"`
}

// TokenBucket represents rate limit state using token bucket algorithm
type TokenBucket struct {
	Tokens     float64   `json:"tokens"`
	Capacity   float64   `json:"capacity"`
	RefillRate float64   `json:"refill_rate"`
	LastRefill time.Time `json:"last_refill"`
}

// Common validation errors
var (
	ErrInvalidName       = errors.New("invalid name: must be 1-255 characters")
	ErrInvalidScope      = errors.New("invalid scope: must be non-empty")
	ErrInvalidModel      = errors.New("invalid model: must be non-empty")
	ErrInvalidProvider   = errors.New("invalid provider: must be non-empty")
	ErrMissingAPIKeyID   = errors.New("missing api_key_id")
	ErrMissingCredential = errors.New("missing encrypted credential")
	ErrInvalidCapacity   = errors.New("invalid capacity: must be positive")
	ErrInvalidRefillRate = errors.New("invalid refill rate: must be positive")
)

// validNameRegex ensures names are alphanumeric with hyphens, underscores
var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Validate validates the APIKey fields
func (k *APIKey) Validate() error {
	if k.ID == "" {
		return errors.New("missing id")
	}
	if k.Name == "" || len(k.Name) > 255 {
		return ErrInvalidName
	}
	if !validNameRegex.MatchString(k.Name) {
		return fmt.Errorf("%w: must contain only alphanumeric characters, hyphens, and underscores", ErrInvalidName)
	}
	if k.Hash == "" {
		return errors.New("missing hash")
	}
	if len(k.Scopes) == 0 {
		return errors.New("missing scopes")
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
		return errors.New("expires_at must be after created_at")
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

// Validate validates the Preset fields
func (p *Preset) Validate() error {
	if p.ID == "" {
		return errors.New("missing id")
	}
	if p.Name == "" || len(p.Name) > 255 {
		return ErrInvalidName
	}
	if !validNameRegex.MatchString(p.Name) {
		return fmt.Errorf("%w: must contain only alphanumeric characters, hyphens, and underscores", ErrInvalidName)
	}
	if p.Version < 1 {
		return errors.New("invalid version: must be at least 1")
	}
	if len(p.Config) == 0 {
		return errors.New("empty config")
	}
	if p.UpdatedAt.Before(p.CreatedAt) {
		return errors.New("updated_at must be after or equal to created_at")
	}
	return nil
}

// Validate validates the BYOKCredential fields
func (c *BYOKCredential) Validate() error {
	if c.APIKeyID == "" {
		return ErrMissingAPIKeyID
	}
	if c.Provider == "" {
		return ErrInvalidProvider
	}
	if c.EncryptedCredential == "" {
		return ErrMissingCredential
	}
	if c.Priority < 0 {
		return errors.New("invalid priority: must be non-negative")
	}
	if c.UpdatedAt.Before(c.CreatedAt) {
		return errors.New("updated_at must be after or equal to created_at")
	}
	if c.LastUsed != nil && c.LastUsed.Before(c.CreatedAt) {
		return errors.New("last_used must be after created_at")
	}
	return nil
}

// Validate validates the DefaultKey fields
func (d *DefaultKey) Validate() error {
	if d.Provider == "" {
		return ErrInvalidProvider
	}
	if d.EncryptedCredential == "" {
		return ErrMissingCredential
	}
	if d.RateLimit != nil {
		if d.RateLimit.RequestsPerMinute < 0 {
			return errors.New("invalid requests_per_minute: must be non-negative")
		}
		if d.RateLimit.TokensPerMinute < 0 {
			return errors.New("invalid tokens_per_minute: must be non-negative")
		}
	}
	if d.UpdatedAt.Before(d.CreatedAt) {
		return errors.New("updated_at must be after or equal to created_at")
	}
	return nil
}

// Validate validates the TokenBucket fields
func (t *TokenBucket) Validate() error {
	if t.Capacity <= 0 {
		return ErrInvalidCapacity
	}
	if t.RefillRate <= 0 {
		return ErrInvalidRefillRate
	}
	if t.Tokens < 0 {
		return errors.New("tokens cannot be negative")
	}
	if t.Tokens > t.Capacity {
		return errors.New("tokens cannot exceed capacity")
	}
	return nil
}

// Refill calculates the current token count after refilling based on elapsed time
func (t *TokenBucket) Refill() {
	now := time.Now()
	elapsed := now.Sub(t.LastRefill).Seconds()
	tokensToAdd := elapsed * t.RefillRate
	
	t.Tokens = t.Tokens + tokensToAdd
	if t.Tokens > t.Capacity {
		t.Tokens = t.Capacity
	}
	t.LastRefill = now
}

// TryConsume attempts to consume the specified number of tokens
// Returns true if successful, false if insufficient tokens
func (t *TokenBucket) TryConsume(tokens float64) bool {
	t.Refill()
	if t.Tokens >= tokens {
		t.Tokens -= tokens
		return true
	}
	return false
}