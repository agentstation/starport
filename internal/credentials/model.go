package credentials

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidScope reports an invalid provider-credential owner scope.
	ErrInvalidScope = errors.New("invalid credential scope")
	// ErrInvalidProvider reports an empty provider identity.
	ErrInvalidProvider = errors.New("invalid provider")
	// ErrMissingCredential reports an empty encrypted credential value.
	ErrMissingCredential = errors.New("missing encrypted credential")
	// ErrInvalidAccess reports an unknown shared-credential access value.
	ErrInvalidAccess = errors.New("invalid shared credential access")
)

// RateLimitConfig defines optional provider-credential limits.
type RateLimitConfig struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	TokensPerMinute   int `json:"tokens_per_minute"`
}

// Access says who may spend one shared credential.
type Access string

const (
	// AccessOpen lets every account spend the credential.
	AccessOpen Access = "open"
	// AccessGranted restricts the credential to the accounts in its grant
	// list. An empty list is a credential granted to nobody yet, which is a
	// valid parked state, not an error.
	AccessGranted Access = "granted"
)

// ParseAccess validates one access value. An empty value selects the open
// default, because a credential an operator applies without saying otherwise
// is for every account.
func ParseAccess(value string) (Access, error) {
	access := Access(value)
	switch access {
	case "":
		return AccessOpen, nil
	case AccessOpen, AccessGranted:
		return access, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidAccess, value)
	}
}

// SharedCredential is one of the operator's provider credentials at the
// shared scope. A provider holds a list of them, so an operator can run
// several keys for one provider and decide per key who may spend it.
type SharedCredential struct {
	ID                  string           `json:"id"`
	Label               string           `json:"label,omitempty"`
	EncryptedCredential string           `json:"encrypted_credential"`
	Config              map[string]any   `json:"config,omitempty"`
	RateLimit           *RateLimitConfig `json:"rate_limit,omitempty"`
	Access              Access           `json:"access"`
	Grants              []string         `json:"grants,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	UpdatedAt           time.Time        `json:"updated_at"`
	LastUsed            *time.Time       `json:"last_used,omitempty"`
	UsageCount          int64            `json:"usage_count"`
}

// Usable reports whether the named account may spend this credential. An
// empty account is an anonymous caller, which only an open credential serves.
func (c SharedCredential) Usable(accountID string) bool {
	switch c.Access {
	case AccessOpen:
		return true
	case AccessGranted:
		if accountID == "" {
			return false
		}
		for _, granted := range c.Grants {
			if granted == accountID {
				return true
			}
		}
	}
	return false
}

// Validate checks shared-credential invariants.
func (c SharedCredential) Validate() error {
	if c.ID == "" {
		return errors.New("shared credential requires an id")
	}
	if c.EncryptedCredential == "" {
		return ErrMissingCredential
	}
	if c.Access != AccessOpen && c.Access != AccessGranted {
		return fmt.Errorf("%w: %q", ErrInvalidAccess, string(c.Access))
	}
	if c.RateLimit != nil && (c.RateLimit.RequestsPerMinute < 0 || c.RateLimit.TokensPerMinute < 0) {
		return errors.New("shared credential rate limits must be non-negative")
	}
	if c.UpdatedAt.Before(c.CreatedAt) {
		return errors.New("updated_at must be after or equal to created_at")
	}
	if c.LastUsed != nil && c.LastUsed.Before(c.CreatedAt) {
		return errors.New("last_used must be after created_at")
	}
	return nil
}

// ProviderKey is one stored provider-credential record. At an account scope
// it holds that account's own encrypted credential. At SharedScope it holds
// the operator's list of shared credentials for the provider instead.
type ProviderKey struct {
	Scope               string             `json:"scope"`
	Provider            string             `json:"provider"`
	EncryptedCredential string             `json:"encrypted_credential,omitempty"`
	Shared              []SharedCredential `json:"shared,omitempty"`
	Config              map[string]any     `json:"config,omitempty"`
	IsFallback          bool               `json:"is_fallback"`
	Priority            int                `json:"priority"`
	RateLimit           *RateLimitConfig   `json:"rate_limit,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	LastUsed            *time.Time         `json:"last_used,omitempty"`
	UsageCount          int64              `json:"usage_count"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

// SharedScope is the scope of the credentials the operator shares with the
// deployment's accounts. Every other scope names one account.
const SharedScope = "*"

// IsShared reports whether this record holds the operator's shared
// credentials. A record at any other scope belongs to one account, which is
// the only kind this gateway calls BYOK.
func (k ProviderKey) IsShared() bool { return k.Scope == SharedScope }

// Validate checks provider-credential invariants. The shared record and the
// account record carry different shapes, and each refuses the other's.
func (k ProviderKey) Validate() error {
	if k.Scope == "" {
		return ErrInvalidScope
	}
	if k.Provider == "" {
		return ErrInvalidProvider
	}
	if k.IsShared() {
		if k.EncryptedCredential != "" {
			return errors.New("a shared record holds its credentials in the shared list")
		}
		if len(k.Shared) == 0 {
			return errors.New("a shared record requires at least one shared credential")
		}
		seen := make(map[string]bool, len(k.Shared))
		for _, credential := range k.Shared {
			if err := credential.Validate(); err != nil {
				return err
			}
			if seen[credential.ID] {
				return fmt.Errorf("duplicate shared credential id %q", credential.ID)
			}
			seen[credential.ID] = true
		}
	} else {
		if len(k.Shared) != 0 {
			return errors.New("only the shared scope holds a shared credential list")
		}
		if k.EncryptedCredential == "" {
			return ErrMissingCredential
		}
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
