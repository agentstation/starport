// Package providers manages API keys for external LLM providers.
package providers

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/models"
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

// Key represents a decrypted provider key with metadata
type Key struct {
	Provider      string                 `json:"provider"`
	Data          map[string]string      `json:"data"`          // Decrypted key data
	Config        map[string]interface{} `json:"config"`        // Provider-specific config
	IsFallback    bool                   `json:"is_fallback"`   // Use as fallback when rate limited
	Priority      int                    `json:"priority"`      // Order preference (lower = higher priority)
	RateLimit     *models.RateLimitConfig `json:"rate_limit,omitempty"` // Rate limits (for global keys)
	CreatedAt     time.Time              `json:"created_at"`
	LastUsed      *time.Time             `json:"last_used"`
	UsageCount    int64                  `json:"usage_count"`
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

// KeyManager interface defines provider key management operations
type KeyManager interface {
	// Key management for user-scoped keys
	AddKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]interface{}, isFallback bool, priority int) (*models.ProviderKey, error)
	GetKey(ctx context.Context, scope, provider string) (*models.ProviderKey, error)
	GetKeys(ctx context.Context, scope, provider string) ([]*models.ProviderKey, error) // Returns all keys for provider sorted by priority
	ListKeys(ctx context.Context, scope string) ([]*models.ProviderKey, error)
	UpdateKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]interface{}, isFallback *bool, priority *int) (*models.ProviderKey, error)
	DeleteKey(ctx context.Context, scope, provider string) error
	ValidateKey(ctx context.Context, provider string, key map[string]string, config map[string]interface{}) error

	// Global key management (scope = "*")
	AddGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]interface{}, rateLimit *models.RateLimitConfig) (*models.ProviderKey, error)
	GetGlobalKey(ctx context.Context, provider string) (*models.ProviderKey, error)
	UpdateGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]interface{}, rateLimit *models.RateLimitConfig) (*models.ProviderKey, error)
	DeleteGlobalKey(ctx context.Context, provider string) error
	ListGlobalKeys(ctx context.Context) ([]*models.ProviderKey, error)

	// Request routing
	DetermineKeyStrategy(ctx context.Context, scope string, provider string) FallbackStrategy
	CalculateProviderKeyCost(usage *Usage) float64
	RecordUsage(ctx context.Context, scope string, provider string, usage *Usage) error

	// Security
	RotateEncryptionKey(ctx context.Context) error
}

// ValidationError represents a key validation failure
type ValidationError struct {
	Provider string
	Field    string
	Message  string
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return "validation failed for " + e.Provider + " " + e.Field + ": " + e.Message
	}
	return "validation failed for " + e.Provider + ": " + e.Message
}

// KeyType indicates which type of key was used for a request
type KeyType string

const (
	// KeyTypeGateway indicates gateway-provided keys were used (global keys)
	KeyTypeGateway KeyType = "gateway"
	// KeyTypeUser indicates user's own keys were used
	KeyTypeUser    KeyType = "user"
)

// Request types for API endpoints

// CreateKeyRequest represents a request to create a provider key
type CreateKeyRequest struct {
	Provider   string                 `json:"provider"`
	Key        map[string]string      `json:"key"`
	Config     map[string]interface{} `json:"config,omitempty"`
	IsFallback bool                   `json:"is_fallback,omitempty"`
	Priority   int                    `json:"priority,omitempty"`
	RateLimit  *models.RateLimitConfig `json:"rate_limit,omitempty"` // For global keys
}

// UpdateKeyRequest represents a request to update a provider key
type UpdateKeyRequest struct {
	Key        map[string]string      `json:"key,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
	IsFallback *bool                  `json:"is_fallback,omitempty"`
	Priority   *int                   `json:"priority,omitempty"`
}

// UpdateGlobalKeyRequest represents a request to update a global provider key
type UpdateGlobalKeyRequest struct {
	Key       map[string]string       `json:"key,omitempty"`
	Config    map[string]interface{}  `json:"config,omitempty"`
	RateLimit *models.RateLimitConfig `json:"rate_limit,omitempty"`
}

// Response types for API endpoints

// KeyResponse represents a provider key in API responses
type KeyResponse struct {
	Scope      string                 `json:"scope"`
	Provider   string                 `json:"provider"`
	Config     map[string]interface{} `json:"config,omitempty"`
	IsFallback bool                   `json:"is_fallback"`
	Priority   int                    `json:"priority"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// GlobalKeyResponse represents a global provider key in API responses
type GlobalKeyResponse struct {
	Provider  string                  `json:"provider"`
	Config    map[string]interface{}  `json:"config,omitempty"`
	RateLimit *models.RateLimitConfig `json:"rate_limit,omitempty"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
}