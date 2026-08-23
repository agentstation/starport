// Package requestctx defines typed request context values shared by the
// server middleware and HTTP controllers.
package requestctx

import (
	"context"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/tenant"
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
	// TenantID stores the account the request runs under. It is distinct from
	// APIKeyID: many keys can belong to one tenant.
	TenantID Key = "tenant_id"
	// TenantRecord stores the account behind the authenticated key. It is the
	// operator's governing record: the credential strategy the request may run
	// under, and the limits it spends against.
	TenantRecord Key = "tenant_record"
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

// WithTenantID stores the request tenant in the context.
func WithTenantID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, TenantID, value)
}

// TenantIDOrDefault returns the account the request runs under, falling back
// to the canonical tenant when no authenticated identity set one.
//
// This is the single place that decides the tenant of a request that carries
// no key. An unauthenticated gateway still has to attribute usage, apply
// limits, and select credentials, and it attributes all of them to the default
// tenant.
func TenantIDOrDefault(ctx context.Context) string {
	if value, ok := ctx.Value(TenantID).(string); ok && value != "" {
		return value
	}
	if apiKey, ok := GetAPIKeyModel(ctx); ok && apiKey != nil {
		return apiKey.EffectiveTenantID()
	}
	return tenant.DefaultID
}

// WithTenantRecord stores the account behind the authenticated key.
func WithTenantRecord(ctx context.Context, value *tenant.Tenant) context.Context {
	return context.WithValue(ctx, TenantRecord, value)
}

// GetTenantRecord returns the account behind the authenticated key. It is
// absent when the deployment could not read the account, which is not an
// authentication failure: the key is still valid and the request falls back to
// the default governing policy.
func GetTenantRecord(ctx context.Context) (*tenant.Tenant, bool) {
	value, ok := ctx.Value(TenantRecord).(*tenant.Tenant)
	return value, ok && value != nil
}

// TenantCredentialStrategyOrDefault returns the credential policy the
// operator set for this request's account. It is the ceiling a per-request
// strategy may narrow and may never widen, so an unreadable account resolves
// to the default rather than to no policy at all.
func TenantCredentialStrategyOrDefault(ctx context.Context) tenant.CredentialStrategy {
	if record, ok := GetTenantRecord(ctx); ok {
		return record.EffectiveCredentialStrategy()
	}
	return tenant.StrategyOperatorFirst
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
