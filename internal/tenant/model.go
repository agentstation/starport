// Package tenant owns the account identity that holds gateway API keys,
// limits, and a provider credential policy. A gateway API key authenticates
// a request. The tenant behind that key owns what the request may reach and
// how much of it the request may spend.
package tenant

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/agentstation/starport/internal/limits"
)

// DefaultID is the canonical tenant. Every deployment has it from first boot,
// and a gateway API key with no explicit tenant belongs to it.
const DefaultID = "default"

// DefaultName is the display name of the canonical tenant.
const DefaultName = "Default"

// CredentialStrategy names which provider credential sources serve this
// tenant, and in which order. The tenant owns this value because it is how
// an operator says whether an account may draw on the deployment's own
// provider credentials. A request may narrow the tenant's strategy. It may
// never widen it.
type CredentialStrategy string

const (
	// StrategyOperatorFirst spends the operator's credentials before the
	// tenant's own: environment, then gateway, then BYOK. It is the default.
	StrategyOperatorFirst CredentialStrategy = "operator_first"
	// StrategyBYOKFirst prefers the tenant's own credential and falls back
	// to the operator's: BYOK, then environment, then gateway.
	StrategyBYOKFirst CredentialStrategy = "byok_first"
	// StrategyBYOKOnly serves this tenant from its own credentials alone. It
	// is how an operator denies an account every operator credential.
	StrategyBYOKOnly CredentialStrategy = "byok_only"
)

// Tenant is one account identity.
type Tenant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Limits is the account-wide cap. It meters the sum over every key this
	// tenant holds, so it is not a per-key ceiling: a key limit bounds one
	// key and never raises or lowers what the account may spend in total. A
	// request satisfies both meters. A nil value sets no account-wide cap.
	Limits *limits.Limits `json:"limits,omitempty"`
	// CredentialStrategy is the default provider credential policy for every
	// request this tenant makes. An empty value reads as StrategyOperatorFirst.
	CredentialStrategy CredentialStrategy `json:"credential_strategy,omitempty"`
	Metadata           map[string]any     `json:"metadata,omitempty"`
	Active             bool               `json:"active"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

var (
	// ErrMissingID reports a tenant without a durable ID.
	ErrMissingID = errors.New("missing tenant id")
	// ErrInvalidID reports a tenant ID outside the public contract.
	ErrInvalidID = errors.New("invalid tenant id: must be 1-255 characters")
	// ErrInvalidName reports an invalid tenant name.
	ErrInvalidName = errors.New("invalid tenant name: must be 1-255 characters")
	// ErrInvalidTimestamps reports an update recorded before creation.
	ErrInvalidTimestamps = errors.New("updated_at must not precede created_at")
	// ErrInvalidCredentialStrategy reports an unknown credential strategy.
	ErrInvalidCredentialStrategy = errors.New(
		"credential strategy must be operator_first, byok_first, or byok_only",
	)
)

// A tenant ID reaches a credential storage key and a request log field, so it
// stays inside the same character set the gateway already accepts for an
// identity name.
var validIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateID checks the public tenant-ID contract.
func ValidateID(id string) error {
	if id == "" || len(id) > 255 {
		return ErrInvalidID
	}
	if !validIDRegex.MatchString(id) {
		return fmt.Errorf(
			"%w: must contain only alphanumeric characters, hyphens, and underscores",
			ErrInvalidID,
		)
	}
	return nil
}

// Validate checks the tenant invariants.
func (t Tenant) Validate() error {
	if t.ID == "" {
		return ErrMissingID
	}
	if err := ValidateID(t.ID); err != nil {
		return err
	}
	if t.Name == "" || len(t.Name) > 255 {
		return ErrInvalidName
	}
	if err := t.Limits.Validate(); err != nil {
		return err
	}
	if !t.CredentialStrategy.Valid() {
		return ErrInvalidCredentialStrategy
	}
	if !t.UpdatedAt.IsZero() && t.UpdatedAt.Before(t.CreatedAt) {
		return ErrInvalidTimestamps
	}
	return nil
}

// IsDefault reports whether this is the canonical tenant.
func (t Tenant) IsDefault() bool { return t.ID == DefaultID }

// EffectiveCredentialStrategy resolves the stored value, treating an unset
// strategy as the default rather than as an error.
func (t Tenant) EffectiveCredentialStrategy() CredentialStrategy {
	if t.CredentialStrategy == "" {
		return StrategyOperatorFirst
	}
	return t.CredentialStrategy
}

// Valid reports whether the strategy names a supported policy. The empty
// value is valid and reads as StrategyOperatorFirst.
func (s CredentialStrategy) Valid() bool {
	switch s {
	case "", StrategyOperatorFirst, StrategyBYOKFirst, StrategyBYOKOnly:
		return true
	}
	return false
}

// AllowsOperatorCredentials reports whether this strategy may reach a
// credential the operator owns, in the environment or at the gateway scope.
func (s CredentialStrategy) AllowsOperatorCredentials() bool {
	return s != StrategyBYOKOnly
}
