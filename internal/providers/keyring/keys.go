// Package keyring manages API keys for external LLM providers.
package keyring

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/failure"
)

const (
	// StrategyMetadataKey is the API-key metadata field that selects inference
	// credential order.
	StrategyMetadataKey = "provider_credential_strategy"
	userScopePrefix     = "user:"
)

// CredentialSource identifies one request-bound inference credential plane.
type CredentialSource string

const (
	// CredentialSourceOperator selects deployment-owned runtime material.
	CredentialSourceOperator CredentialSource = "operator"
	// CredentialSourceUser selects the authenticated tenant's stored material.
	CredentialSourceUser CredentialSource = "user"
)

// Strategy defines request-bound inference credential order.
type Strategy string

const (
	// OperatorFirst tries deployment-owned material before tenant material.
	OperatorFirst Strategy = "operator_first"
	// UserFirst tries tenant material before deployment-owned material.
	UserFirst Strategy = "user_first"
	// UserOnly uses only tenant material.
	UserOnly Strategy = "user_only"
)

// ErrInvalidStrategy reports an unknown request credential strategy.
var ErrInvalidStrategy = errors.New("invalid provider credential strategy")

// ParseStrategy validates one exact strategy value. An empty value selects the
// default operator-first policy.
func ParseStrategy(value string) (Strategy, error) {
	strategy := Strategy(value)
	if strategy == "" {
		return OperatorFirst, nil
	}
	switch strategy {
	case OperatorFirst, UserFirst, UserOnly:
		return strategy, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidStrategy, value)
	}
}

// StrategyFromMetadata reads the strategy from authenticated identity
// metadata without accepting non-string values.
func StrategyFromMetadata(metadata map[string]any) (Strategy, error) {
	value, exists := metadata[StrategyMetadataKey]
	if !exists {
		return OperatorFirst, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: metadata value must be a string", ErrInvalidStrategy)
	}
	return ParseStrategy(text)
}

// Sources returns a caller-owned credential order.
func (s Strategy) Sources() []CredentialSource {
	switch s {
	case UserFirst:
		return []CredentialSource{CredentialSourceUser, CredentialSourceOperator}
	case UserOnly:
		return []CredentialSource{CredentialSourceUser}
	default:
		return []CredentialSource{CredentialSourceOperator, CredentialSourceUser}
	}
}

// CanAdvance reports whether credential policy can try the next source after
// the current failure. Not-configured resolution is represented separately by
// the caller and always permits an available next source.
func CanAdvance(providerFailure *failure.Failure) bool {
	if providerFailure == nil || providerFailure.StateScope() != failure.ScopeCredential {
		return false
	}
	switch providerFailure.Kind() {
	case failure.Authentication, failure.Permission, failure.Quota,
		failure.Billing, failure.RateLimit:
		return true
	default:
		return false
	}
}

// UnavailableFailure returns the one external credential-availability shape.
// Its message does not depend on operator material or internal failure detail.
func UnavailableFailure(providerID string, cause error) *failure.Failure {
	return failure.New(
		failure.Authentication,
		"Provider credentials are not configured.",
		false,
		failure.ProviderDetails{Provider: providerID},
		cause,
	)
}

// UserScope returns the exact credential repository scope for one tenant.
func UserScope(tenantID string) string { return userScopePrefix + tenantID }

// ProviderKey represents a decrypted provider key with metadata
type ProviderKey struct {
	Provider   string                       `json:"provider"`
	Data       map[string]string            `json:"data"`                 // Decrypted key data
	Config     map[string]any               `json:"config"`               // Provider-specific config
	IsFallback bool                         `json:"is_fallback"`          // Use as fallback when rate limited
	Priority   int                          `json:"priority"`             // Order preference (lower = higher priority)
	RateLimit  *credentials.RateLimitConfig `json:"rate_limit,omitempty"` // Rate limits (for global keys)
	CreatedAt  time.Time                    `json:"created_at"`
	LastUsed   *time.Time                   `json:"last_used"`
	UsageCount int64                        `json:"usage_count"`
}

// Usage represents usage metrics for cost calculation
type Usage struct {
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	ImageCount       int       `json:"image_count,omitempty"`
	AudioSeconds     float64   `json:"audio_seconds,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
}

// ProviderKeys interface defines provider key management operations
type ProviderKeys interface {
	// Key management for user-scoped keys
	AddKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback bool, priority int) (*credentials.ProviderKey, error)
	GetKey(ctx context.Context, scope, provider string) (*credentials.ProviderKey, error)
	GetKeys(ctx context.Context, scope, provider string) ([]*credentials.ProviderKey, error) // Returns all keys for provider sorted by priority
	ListKeys(ctx context.Context, scope string) ([]*credentials.ProviderKey, error)
	UpdateKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback *bool, priority *int) (*credentials.ProviderKey, error)
	DeleteKey(ctx context.Context, scope, provider string) error
	ValidateKey(ctx context.Context, provider string, key map[string]string, config map[string]any) error
	ResolveUserMaterial(ctx context.Context, scope string, provider catalogs.Provider) (credentials.Material, error)

	// Global key management (scope = "*")
	AddGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]any, rateLimit *credentials.RateLimitConfig) (*credentials.ProviderKey, error)
	GetGlobalKey(ctx context.Context, provider string) (*credentials.ProviderKey, error)
	UpdateGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]any, rateLimit *credentials.RateLimitConfig) (*credentials.ProviderKey, error)
	DeleteGlobalKey(ctx context.Context, provider string) error
	ListGlobalKeys(ctx context.Context) ([]*credentials.ProviderKey, error)

	// Request accounting
	RecordUsage(ctx context.Context, scope string, provider string, usage *Usage) error

	// Security
	RotateEncryptionKey(ctx context.Context) error
}

// ValidationError reports an invalid catalog-declared inference credential.
type ValidationError struct {
	Provider string
	Field    string
	Message  string
}

// Error returns a secret-free validation message.
func (e *ValidationError) Error() string {
	if e.Field != "" {
		return "validation failed for " + e.Provider + " " + e.Field + ": " + e.Message
	}
	return "validation failed for " + e.Provider + ": " + e.Message
}
