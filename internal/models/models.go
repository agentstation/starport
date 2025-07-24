// Package models provides data models for the Starport gateway.
package models

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

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

// ProviderKey represents an encrypted API key for an external LLM provider
// Keys can be scoped to specific users (scope = "user:id") or globally (scope = "*")
type ProviderKey struct {
	Scope               string                 `json:"scope"` // "*" for global, "user:id" for user-specific
	Provider            string                 `json:"provider"`
	EncryptedCredential string                 `json:"encrypted_credential"`
	Config              map[string]interface{} `json:"config,omitempty"`     // Provider-specific config (endpoints, versions, etc)
	IsFallback          bool                   `json:"is_fallback"`          // Use as fallback when rate limited
	Priority            int                    `json:"priority"`             // Order preference (lower = higher priority)
	RateLimit           *RateLimitConfig       `json:"rate_limit,omitempty"` // Rate limits (typically for global keys)
	CreatedAt           time.Time              `json:"created_at"`
	LastUsed            *time.Time             `json:"last_used,omitempty"`
	UsageCount          int64                  `json:"usage_count"`
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
	ErrInvalidProvider   = errors.New("invalid provider: must be non-empty")
	ErrMissingCredential = errors.New("missing encrypted credential")
	ErrInvalidCapacity   = errors.New("invalid capacity: must be positive")
	ErrInvalidRefillRate = errors.New("invalid refill rate: must be positive")
)

// validNameRegex ensures names are alphanumeric with hyphens, underscores
var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

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

// IsGlobal returns true if this is a gateway-wide key
func (k *ProviderKey) IsGlobal() bool {
	return k.Scope == "*"
}

// Validate validates the ProviderKey fields
func (k *ProviderKey) Validate() error {
	// Scope can be "*" for global or "user:id" for user-specific
	// Empty scope defaults to global for backward compatibility
	if k.Scope == "" {
		k.Scope = "*"
	}
	if k.Provider == "" {
		return ErrInvalidProvider
	}
	if k.EncryptedCredential == "" {
		return ErrMissingCredential
	}
	if k.Priority < 0 {
		return errors.New("invalid priority: must be non-negative")
	}
	if k.RateLimit != nil {
		if k.RateLimit.RequestsPerMinute < 0 {
			return errors.New("invalid requests_per_minute: must be non-negative")
		}
		if k.RateLimit.TokensPerMinute < 0 {
			return errors.New("invalid tokens_per_minute: must be non-negative")
		}
	}
	if k.UpdatedAt.Before(k.CreatedAt) {
		return errors.New("updated_at must be after or equal to created_at")
	}
	if k.LastUsed != nil && k.LastUsed.Before(k.CreatedAt) {
		return errors.New("last_used must be after created_at")
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
