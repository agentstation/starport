package credentials

import (
	"errors"
	"time"
)

var (
	// ErrInvalidScope reports an invalid provider-credential owner scope.
	ErrInvalidScope = errors.New("invalid credential scope")
	// ErrInvalidProvider reports an empty provider identity.
	ErrInvalidProvider = errors.New("invalid provider")
	// ErrMissingCredential reports an empty encrypted credential value.
	ErrMissingCredential = errors.New("missing encrypted credential")
)

// RateLimitConfig defines optional provider-credential limits.
type RateLimitConfig struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	TokensPerMinute   int `json:"tokens_per_minute"`
}

// ProviderKey is one encrypted external provider credential.
type ProviderKey struct {
	Scope               string           `json:"scope"`
	Provider            string           `json:"provider"`
	EncryptedCredential string           `json:"encrypted_credential"`
	Config              map[string]any   `json:"config,omitempty"`
	IsFallback          bool             `json:"is_fallback"`
	Priority            int              `json:"priority"`
	RateLimit           *RateLimitConfig `json:"rate_limit,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	LastUsed            *time.Time       `json:"last_used,omitempty"`
	UsageCount          int64            `json:"usage_count"`
	UpdatedAt           time.Time        `json:"updated_at"`
}

// GatewayScope is the scope of a credential the operator applies once for the
// whole deployment. Every other scope names one tenant.
const GatewayScope = "*"

// IsGateway reports whether the operator owns this credential for the whole
// deployment. A credential at any other scope belongs to one tenant, which is
// the only kind this gateway calls BYOK.
func (k ProviderKey) IsGateway() bool { return k.Scope == GatewayScope }

// Validate checks provider-credential invariants.
func (k ProviderKey) Validate() error {
	if k.Scope == "" {
		return ErrInvalidScope
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
	if k.RateLimit != nil && (k.RateLimit.RequestsPerMinute < 0 || k.RateLimit.TokensPerMinute < 0) {
		return errors.New("provider credential rate limits must be non-negative")
	}
	if k.UpdatedAt.Before(k.CreatedAt) {
		return errors.New("updated_at must be after or equal to created_at")
	}
	if k.LastUsed != nil && k.LastUsed.Before(k.CreatedAt) {
		return errors.New("last_used must be after created_at")
	}
	return nil
}
