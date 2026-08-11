// Package byok manages API keys for external LLM providers.
package byok

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/credentials"
)

// ProviderKeyType indicates which type of key was used for a request
type ProviderKeyType string

const (
	// ProviderKeyTypeGateway indicates gateway-provided keys were used (global keys)
	ProviderKeyTypeGateway ProviderKeyType = "gateway"
	// ProviderKeyTypeUser indicates user's own keys were used
	ProviderKeyTypeUser ProviderKeyType = "user"
)

// FallbackStrategy defines how to handle key selection between user and gateway keys
type FallbackStrategy string

const (
	// GatewayFirst uses gateway keys initially, falls back to user keys on rate limit
	GatewayFirst FallbackStrategy = "gateway_first"
	// UserKeyFirst prefers user's keys, falls back to gateway if user key fails
	UserKeyFirst FallbackStrategy = "user_first"
	// UserKeyOnly never uses gateway keys, fails if user key unavailable
	UserKeyOnly FallbackStrategy = "user_only"
)

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

	// Global key management (scope = "*")
	AddGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]any, rateLimit *credentials.RateLimitConfig) (*credentials.ProviderKey, error)
	GetGlobalKey(ctx context.Context, provider string) (*credentials.ProviderKey, error)
	UpdateGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]any, rateLimit *credentials.RateLimitConfig) (*credentials.ProviderKey, error)
	DeleteGlobalKey(ctx context.Context, provider string) error
	ListGlobalKeys(ctx context.Context) ([]*credentials.ProviderKey, error)

	// Request routing
	DetermineKeyStrategy(ctx context.Context, scope string, provider string) FallbackStrategy
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
