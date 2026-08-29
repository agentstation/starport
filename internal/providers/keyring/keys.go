// Package keyring stores and resolves the provider credentials a request can
// spend. Three sources feed it, and two of the three belong to the operator:
// a credential read from the process environment, a credential the operator
// shares with the deployment's accounts at the shared scope, and a credential
// an account brings for itself. Only the last of those is BYOK, which is why
// the package is not named for it.
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
	accountScopePrefix  = "account:"
	// SharedScope is the credential scope that holds the operator's shared
	// credentials. Each credential there is open to every account or granted
	// to some. The record layer owns the value, so the two can never drift
	// apart.
	SharedScope = credentials.SharedScope
)

// CredentialSource identifies one request-bound inference credential plane.
// The first two planes hold operator credentials. Only SourceBYOK holds a
// credential an account brought for itself.
type CredentialSource string

const (
	// SourceEnvironment selects a credential the deployment read from its
	// process environment.
	SourceEnvironment CredentialSource = "environment"
	// SourceShared selects a credential the operator shared with the
	// deployment's accounts at SharedScope.
	SourceShared CredentialSource = "shared"
	// SourceBYOK selects the credential the request's own account brought.
	SourceBYOK CredentialSource = "byok"
	// SourceAnonymous names an attempt a provider accepted with no credential
	// at all. No strategy selects it and no plane holds it; it is here so that
	// a record of what paid for a request can say "nothing did" instead of
	// saying nothing, which is what an unrecorded request also says.
	SourceAnonymous CredentialSource = "anonymous"
)

// Strategy defines request-bound inference credential order.
type Strategy string

const (
	// OperatorFirst spends the operator's credentials before the account's own.
	OperatorFirst Strategy = "operator_first"
	// BYOKFirst prefers the account's own credential and falls back to the
	// operator's.
	BYOKFirst Strategy = "byok_first"
	// BYOKOnly serves the request from the account's own credential alone. It
	// is how an operator denies an account every operator credential.
	BYOKOnly Strategy = "byok_only"
)

var (
	// ErrInvalidStrategy reports an unknown request credential strategy.
	ErrInvalidStrategy = errors.New("invalid provider credential strategy")
	// ErrStrategyWidens reports a per-request strategy that would reach a
	// credential source the account's own strategy withholds.
	ErrStrategyWidens = errors.New(
		"request credential strategy may narrow the account's, not widen it",
	)
)

// ParseStrategy validates one exact strategy value. An empty value selects the
// default operator-first policy.
func ParseStrategy(value string) (Strategy, error) {
	strategy := Strategy(value)
	if strategy == "" {
		return OperatorFirst, nil
	}
	switch strategy {
	case OperatorFirst, BYOKFirst, BYOKOnly:
		return strategy, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidStrategy, value)
	}
}

// AllowsOperatorCredentials reports whether this strategy may reach a
// credential the operator owns, in the environment or at the shared scope.
func (s Strategy) AllowsOperatorCredentials() bool { return s != BYOKOnly }

// Narrow resolves the strategy one request actually runs under. The account's
// strategy is the ceiling because it is how an operator says whether an
// account may spend the deployment's own money. A request may reorder the
// sources it is already allowed and may give up an operator credential it
// holds, but a request that would reach a source the account withholds is
// refused rather than silently downgraded.
func Narrow(accountStrategy, requestStrategy Strategy) (Strategy, error) {
	if !accountStrategy.AllowsOperatorCredentials() && requestStrategy.AllowsOperatorCredentials() {
		return "", fmt.Errorf(
			"%w: account is %q and the request asked for %q",
			ErrStrategyWidens, accountStrategy, requestStrategy,
		)
	}
	return requestStrategy, nil
}

// EffectiveStrategy resolves the strategy one request actually runs under,
// from the account's governing strategy and the authenticated key's own
// metadata. A key that names no strategy inherits the account's, so an
// operator who denies an account every operator credential does not have to
// stamp every key it holds. A key that names one may narrow the account's and
// never widen it.
func EffectiveStrategy(governing Strategy, metadata map[string]any) (Strategy, error) {
	value, exists := metadata[StrategyMetadataKey]
	if !exists {
		return governing, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: metadata value must be a string", ErrInvalidStrategy)
	}
	requested, err := ParseStrategy(text)
	if err != nil {
		return "", err
	}
	return Narrow(governing, requested)
}

// Sources returns a caller-owned credential order. The two operator planes
// stay adjacent in every order, because an environment credential and a
// shared credential are the same operator's money.
func (s Strategy) Sources() []CredentialSource {
	switch s {
	case BYOKFirst:
		return []CredentialSource{SourceBYOK, SourceEnvironment, SourceShared}
	case BYOKOnly:
		return []CredentialSource{SourceBYOK}
	default:
		return []CredentialSource{SourceEnvironment, SourceShared, SourceBYOK}
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

// AccountScope returns the exact credential repository scope that holds one
// account's BYOK credentials. The scope names the account, never a gateway API
// key, so deleting a key never strands the credentials its account applied.
func AccountScope(accountID string) string { return accountScopePrefix + accountID }

// ProviderKey represents a decrypted provider key with metadata
type ProviderKey struct {
	Provider   string                       `json:"provider"`
	Data       map[string]string            `json:"data"`                 // Decrypted key data
	Config     map[string]any               `json:"config"`               // Provider-specific config
	IsFallback bool                         `json:"is_fallback"`          // Use as fallback when rate limited
	Priority   int                          `json:"priority"`             // Order preference (lower = higher priority)
	RateLimit  *credentials.RateLimitConfig `json:"rate_limit,omitempty"` // Rate limits (for shared credentials)
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

// SharedCredentialParams carries the sharing facts for one new shared
// credential. A zero Access selects the open default: a credential the
// operator applies without saying otherwise serves every account.
type SharedCredentialParams struct {
	Label     string
	Access    credentials.Access
	Grants    []string
	RateLimit *credentials.RateLimitConfig
}

// SharedCredentialUpdate mutates one shared credential. A nil field leaves
// its value alone; Grants replaces the whole grant list because a grant
// change is a statement of who may spend, not a diff.
type SharedCredentialUpdate struct {
	Key       map[string]string
	Config    map[string]any
	Label     *string
	Access    *credentials.Access
	Grants    *[]string
	RateLimit *credentials.RateLimitConfig
}

// ProviderKeys interface defines provider key management operations
type ProviderKeys interface {
	// Account key management. A scope names one account; the shared plane
	// has its own methods because its record holds a list, not one value.
	AddKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback bool, priority int) (*credentials.ProviderKey, error)
	GetKey(ctx context.Context, scope, provider string) (*credentials.ProviderKey, error)
	GetKeys(ctx context.Context, scope, provider string) ([]*credentials.ProviderKey, error) // Returns all keys for provider sorted by priority
	ListKeys(ctx context.Context, scope string) ([]*credentials.ProviderKey, error)
	UpdateKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]any, isFallback *bool, priority *int) (*credentials.ProviderKey, error)
	DeleteKey(ctx context.Context, scope, provider string) error
	ValidateKey(ctx context.Context, provider string, key map[string]string, config map[string]any) error
	// ResolveStoredMaterial decrypts one exact account record against the
	// provider contract.
	ResolveStoredMaterial(ctx context.Context, scope string, provider catalogs.Provider) (credentials.Material, error)

	// Shared credential management (scope = SharedScope). One provider holds
	// a list of shared credentials, each open to every account or granted to
	// some, addressed by id.
	AddSharedCredential(ctx context.Context, provider string, key map[string]string, config map[string]any, params SharedCredentialParams) (*credentials.SharedCredential, error)
	GetSharedCredentials(ctx context.Context, provider string) ([]credentials.SharedCredential, error)
	UpdateSharedCredential(ctx context.Context, provider, credentialID string, update SharedCredentialUpdate) (*credentials.SharedCredential, error)
	DeleteSharedCredential(ctx context.Context, provider, credentialID string) error
	ListShared(ctx context.Context) ([]*credentials.ProviderKey, error)
	// ResolveSharedMaterial decrypts the first shared credential the named
	// account may spend: open, or granted to that account.
	ResolveSharedMaterial(ctx context.Context, accountID string, provider catalogs.Provider) (credentials.Material, error)

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
