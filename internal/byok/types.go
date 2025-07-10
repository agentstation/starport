// Package byok provides bring-your-own-key functionality for the Starport gateway.
package byok

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/models"
)

// FallbackStrategy defines how to handle key selection between BYOK and gateway keys
type FallbackStrategy string

const (
	// GatewayFirst uses gateway keys/credits initially, falls back to BYOK on rate limit
	GatewayFirst FallbackStrategy = "gateway_first"
	// BYOKFirst prefers customer's keys, falls back to gateway if BYOK fails
	BYOKFirst FallbackStrategy = "byok_first"
	// BYOKOnly never uses gateway keys, fails if BYOK unavailable
	BYOKOnly FallbackStrategy = "byok_only"
)

// Credential represents a decrypted BYOK credential with metadata
type Credential struct {
	Provider      string                 `json:"provider"`
	Data          map[string]string      `json:"data"`          // Decrypted credential data
	Config        map[string]interface{} `json:"config"`        // Provider-specific config
	IsFallback    bool                   `json:"is_fallback"`   // Use as fallback when rate limited
	Priority      int                    `json:"priority"`      // Order preference (lower = higher priority)
	RateLimit     *models.RateLimitConfig `json:"rate_limit,omitempty"` // Rate limits (for global credentials)
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

// Manager interface defines BYOK credential management operations
type Manager interface {
	// Credential management
	AddCredential(ctx context.Context, apiKeyID, provider string, cred map[string]string, config map[string]interface{}) error
	GetCredential(ctx context.Context, apiKeyID, provider string) (*Credential, error)
	GetCredentials(ctx context.Context, apiKeyID, provider string) ([]*Credential, error) // Returns all credentials for provider sorted by priority
	ListCredentials(ctx context.Context, apiKeyID string) ([]*Credential, error)
	UpdateCredential(ctx context.Context, apiKeyID, provider string, cred map[string]string, config map[string]interface{}) error
	DeleteCredential(ctx context.Context, apiKeyID, provider string) error
	ValidateCredential(ctx context.Context, provider string, cred map[string]string, config map[string]interface{}) error

	// Global credential management (replaces default keys)
	SetGlobalCredential(ctx context.Context, provider string, cred map[string]string, config map[string]interface{}, rateLimit *models.RateLimitConfig) error
	GetGlobalCredential(ctx context.Context, provider string) (*Credential, error)
	DeleteGlobalCredential(ctx context.Context, provider string) error
	ListGlobalCredentials(ctx context.Context) ([]*Credential, error)

	// Request routing
	DetermineKeyStrategy(ctx context.Context, apiKeyID string, provider string) FallbackStrategy
	CalculateBYOKCost(usage *Usage) float64
	RecordUsage(ctx context.Context, apiKeyID string, provider string, usage *Usage) error

	// Security
	RotateEncryptionKey(ctx context.Context) error
}

// ValidationError represents a credential validation failure
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
	// KeyTypeGateway indicates gateway-provided keys were used (global BYOK)
	KeyTypeGateway KeyType = "gateway"
	// KeyTypeBYOK indicates user's own keys were used
	KeyTypeBYOK    KeyType = "byok"
)