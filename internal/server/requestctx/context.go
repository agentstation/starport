// Package requestctx defines typed request context values shared by the
// server middleware and HTTP controllers.
package requestctx

import (
	"context"

	"github.com/agentstation/starport/internal/identity"
)

// Key is the typed context key used for server request metadata.
type Key string

const (
	// APIKey stores the raw API key for downstream provider-key lookups.
	APIKey Key = "api_key"
	// APIKeyID stores the authenticated Starport API key ID.
	APIKeyID Key = "api_key_id"
	// APIKeyModel stores the authenticated API key model.
	APIKeyModel Key = "api_key_model" // #nosec G101 - context key name, not a credential.
)

// WithAPIKey stores the raw API key in the context.
func WithAPIKey(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, APIKey, value)
}

// WithAPIKeyID stores the API key ID in the context.
func WithAPIKeyID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, APIKeyID, value)
}

// WithAPIKeyModel stores the API key model in the context.
func WithAPIKeyModel(ctx context.Context, value *identity.APIKey) context.Context {
	return context.WithValue(ctx, APIKeyModel, value)
}

// GetAPIKey returns the raw API key from the context.
func GetAPIKey(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(APIKey).(string)
	return value, ok
}

// GetAPIKeyID returns the API key ID from the context.
func GetAPIKeyID(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(APIKeyID).(string)
	return value, ok
}

// GetAPIKeyModel returns the API key model from the context.
func GetAPIKeyModel(ctx context.Context) (*identity.APIKey, bool) {
	value, ok := ctx.Value(APIKeyModel).(*identity.APIKey)
	return value, ok
}
