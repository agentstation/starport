// Package requestctx defines typed request context values shared by the
// server middleware and HTTP controllers.
package requestctx

import (
	"context"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
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
	// AccountID stores the account the request runs under. It is distinct from
	// APIKeyID: many keys can belong to one account.
	AccountID Key = "account_id"
	// AccountRecord stores the account behind the authenticated key. It is the
	// operator's governing record: the credential strategy the request may run
	// under, and the limits it spends against.
	AccountRecord Key = "account_record"
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
func WithAPIKeyModel(ctx context.Context, value *apikey.APIKey) context.Context {
	return context.WithValue(ctx, APIKeyModel, value)
}

// WithAccountID stores the request account in the context.
func WithAccountID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, AccountID, value)
}

// AccountIDOrDefault returns the account the request runs under, falling back
// to the canonical account when no authenticated caller set one.
//
// This is the single place that decides the account of a request that carries
// no key. An unauthenticated gateway still has to attribute usage, apply
// limits, and select credentials, and it attributes all of them to the default
// account.
func AccountIDOrDefault(ctx context.Context) string {
	if value, ok := ctx.Value(AccountID).(string); ok && value != "" {
		return value
	}
	if apiKey, ok := GetAPIKeyModel(ctx); ok && apiKey != nil {
		return apiKey.EffectiveAccountID()
	}
	return account.DefaultID
}

// WithAccountRecord stores the account behind the authenticated key.
func WithAccountRecord(ctx context.Context, value *account.Account) context.Context {
	return context.WithValue(ctx, AccountRecord, value)
}

// GetAccountRecord returns the account behind the authenticated key. It is
// absent when the deployment could not read the account, which is not an
// authentication failure: the key is still valid and the request falls back to
// the default governing policy.
func GetAccountRecord(ctx context.Context) (*account.Account, bool) {
	value, ok := ctx.Value(AccountRecord).(*account.Account)
	return value, ok && value != nil
}

// AccountCredentialStrategyOrDefault returns the credential policy the
// operator set for this request's account. It is the ceiling a per-request
// strategy may narrow and may never widen, so an unreadable account resolves
// to the default rather than to no policy at all.
func AccountCredentialStrategyOrDefault(ctx context.Context) account.CredentialStrategy {
	if record, ok := GetAccountRecord(ctx); ok {
		return record.EffectiveCredentialStrategy()
	}
	return account.StrategyOperatorFirst
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
func GetAPIKeyModel(ctx context.Context) (*apikey.APIKey, bool) {
	value, ok := ctx.Value(APIKeyModel).(*apikey.APIKey)
	return value, ok
}
