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
	// ConsoleGrant stores the grant kind that minted a console session, when
	// the request arrived with one. The audit trail reads it to name the
	// actor behind a console mutation.
	ConsoleGrant Key = "console_grant"
	// ConsoleSubject stores who an identity provider said the console caller
	// is. It is empty for the machine-local grants, which prove where the
	// caller is and not who.
	ConsoleSubject Key = "console_subject"
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

// WithConsoleSession stores the grant kind and identity subject of the
// console session a request arrived with.
func WithConsoleSession(ctx context.Context, grant, subject string) context.Context {
	ctx = context.WithValue(ctx, ConsoleGrant, grant)
	return context.WithValue(ctx, ConsoleSubject, subject)
}

// GetConsoleSession returns the console session's grant kind and identity
// subject. The second return is false when the request carried no console
// session.
func GetConsoleSession(ctx context.Context) (grant, subject string, ok bool) {
	grant, ok = ctx.Value(ConsoleGrant).(string)
	if !ok || grant == "" {
		return "", "", false
	}
	subject, _ = ctx.Value(ConsoleSubject).(string)
	return grant, subject, true
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

// GetTeamID returns the team the serving key is attributed to. It is empty
// for a teamless key and for a request that carries no key model, so team
// attribution and the team budget both read one derivation.
func GetTeamID(ctx context.Context) string {
	if apiKey, ok := GetAPIKeyModel(ctx); ok && apiKey != nil {
		return apiKey.TeamID
	}
	return ""
}
