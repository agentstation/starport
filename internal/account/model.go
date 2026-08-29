// Package account owns the account identity that holds gateway API keys,
// limits, and a provider credential policy. A gateway API key authenticates
// a request. The account behind that key owns what the request may reach and
// how much of it the request may spend.
package account

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/agentstation/starport/internal/limits"
)

// DefaultID is the canonical account. Every deployment has it from first boot,
// and a gateway API key with no explicit account belongs to it.
const DefaultID = "default"

// DefaultName is the display name of the canonical account.
const DefaultName = "Default"

// DefaultOutstandingJobs bounds how many asynchronous jobs one account may
// hold open when it sets no bound of its own.
//
// A job is the one operation this gateway starts and cannot price until later,
// so an unbounded account can hold an unbounded spend commitment open against a
// provider. Every other limit meters work that already finished. A default
// therefore has to exist, because an absent one reads as unlimited on the only
// dimension where unlimited costs money nothing has counted yet.
//
// Eight is a working number for one operator, not a provider fact. A video
// takes minutes, so eight in flight keeps an interactive caller from waiting
// while it stays far under what a provider queues.
const DefaultOutstandingJobs int64 = 8

// CredentialStrategy names which provider credential sources serve this
// account, and in which order. The account owns this value because it is how
// an operator says whether an account may draw on the deployment's own
// provider credentials. A request may narrow the account's strategy. It may
// never widen it.
type CredentialStrategy string

const (
	// StrategyOperatorFirst spends the operator's credentials before the
	// account's own: environment, then gateway, then BYOK. It is the default.
	StrategyOperatorFirst CredentialStrategy = "operator_first"
	// StrategyBYOKFirst prefers the account's own credential and falls back
	// to the operator's: BYOK, then environment, then gateway.
	StrategyBYOKFirst CredentialStrategy = "byok_first"
	// StrategyBYOKOnly serves this account from its own credentials alone. It
	// is how an operator denies an account every operator credential.
	StrategyBYOKOnly CredentialStrategy = "byok_only"
)

// BYOKMode names how widely an account may bring its own provider
// credential. The strategy above says when a BYOK credential is spent;
// this policy says whether one may exist at all for a given provider.
type BYOKMode string

const (
	// BYOKAll lets the account store its own credential for every provider.
	// It is what a nil policy means, because it is today's behavior.
	BYOKAll BYOKMode = "all"
	// BYOKSelected lets the account store its own credential only for the
	// providers the policy names.
	BYOKSelected BYOKMode = "selected"
	// BYOKNone forbids the account every credential of its own.
	BYOKNone BYOKMode = "none"
)

// BYOKPolicy is the operator's answer to whether an account may bring its
// own provider credential, for all providers or for a selected set.
type BYOKPolicy struct {
	Mode BYOKMode `json:"mode"`
	// Providers names the allowed set when Mode is BYOKSelected. The other
	// modes carry no list, because "all" and "none" name every provider
	// already.
	Providers []string `json:"providers,omitempty"`
}

// ProviderAccess grants the account one provider, and optionally narrows
// which of its models. An empty Models list grants every model the provider
// serves, so the default stays "all models of each allowed provider".
type ProviderAccess struct {
	Provider string   `json:"provider"`
	Models   []string `json:"models,omitempty"`
}

// Account is one account identity.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Limits is the account-wide cap. It meters the sum over every key this
	// account holds, so it is not a per-key ceiling: a key limit bounds one
	// key and never raises or lowers what the account may spend in total. A
	// request satisfies both meters. A nil value sets no account-wide cap.
	Limits *limits.Limits `json:"limits,omitempty"`
	// CredentialStrategy is the default provider credential policy for every
	// request this account makes. An empty value reads as StrategyOperatorFirst.
	CredentialStrategy CredentialStrategy `json:"credential_strategy,omitempty"`
	// BYOKPolicy says which providers this account may bring its own
	// credential for. A nil policy allows every provider, which is how every
	// account behaved before the policy existed.
	BYOKPolicy *BYOKPolicy `json:"byok_policy,omitempty"`
	// Access names the providers this account may reach, each entry
	// optionally narrowed to specific models. A nil or empty list grants
	// every provider and every model.
	Access    []ProviderAccess `json:"access,omitempty"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
	Active    bool             `json:"active"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

var (
	// ErrMissingID reports an account without a durable ID.
	ErrMissingID = errors.New("missing account id")
	// ErrInvalidID reports an account ID outside the public contract.
	ErrInvalidID = errors.New("invalid account id: must be 1-255 characters")
	// ErrInvalidName reports an invalid account name.
	ErrInvalidName = errors.New("invalid account name: must be 1-255 characters")
	// ErrInvalidTimestamps reports an update recorded before creation.
	ErrInvalidTimestamps = errors.New("updated_at must not precede created_at")
	// ErrInvalidCredentialStrategy reports an unknown credential strategy.
	ErrInvalidCredentialStrategy = errors.New(
		"credential strategy must be operator_first, byok_first, or byok_only",
	)
	// ErrInvalidBYOKPolicy reports a BYOK policy outside the contract: an
	// unknown mode, a selected mode without providers, or a provider list on
	// a mode that names every provider already.
	ErrInvalidBYOKPolicy = errors.New(
		"byok policy mode must be all, selected, or none, with providers only for selected",
	)
	// ErrInvalidProviderAccess reports an access entry without a provider or
	// a duplicate provider across entries.
	ErrInvalidProviderAccess = errors.New(
		"provider access entries must each name a distinct provider",
	)
)

// An account ID reaches a credential storage key and a request log field, so it
// stays inside the same character set the gateway already accepts for an
// identity name.
var validIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateID checks the public account-ID contract.
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

// Validate checks the account invariants.
func (t Account) Validate() error {
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
	if err := t.BYOKPolicy.Validate(); err != nil {
		return err
	}
	if err := validateProviderAccess(t.Access); err != nil {
		return err
	}
	if !t.UpdatedAt.IsZero() && t.UpdatedAt.Before(t.CreatedAt) {
		return ErrInvalidTimestamps
	}
	return nil
}

// Validate checks the BYOK policy contract. A nil policy is valid and allows
// every provider.
func (p *BYOKPolicy) Validate() error {
	if p == nil {
		return nil
	}
	switch p.Mode {
	case BYOKAll, BYOKNone:
		if len(p.Providers) > 0 {
			return ErrInvalidBYOKPolicy
		}
	case BYOKSelected:
		if len(p.Providers) == 0 {
			return ErrInvalidBYOKPolicy
		}
		for _, provider := range p.Providers {
			if provider == "" {
				return ErrInvalidBYOKPolicy
			}
		}
	default:
		return ErrInvalidBYOKPolicy
	}
	return nil
}

func validateProviderAccess(access []ProviderAccess) error {
	seen := make(map[string]struct{}, len(access))
	for _, entry := range access {
		if entry.Provider == "" {
			return ErrInvalidProviderAccess
		}
		if _, dup := seen[entry.Provider]; dup {
			return ErrInvalidProviderAccess
		}
		seen[entry.Provider] = struct{}{}
	}
	return nil
}

// AllowsBYOK reports whether this account may store its own credential for
// the named provider. A nil policy allows every provider.
func (t Account) AllowsBYOK(provider string) bool {
	if t.BYOKPolicy == nil {
		return true
	}
	switch t.BYOKPolicy.Mode {
	case BYOKAll:
		return true
	case BYOKNone:
		return false
	case BYOKSelected:
		for _, allowed := range t.BYOKPolicy.Providers {
			if allowed == provider {
				return true
			}
		}
	}
	return false
}

// AllowsProvider reports whether this account may reach the named provider.
// An account without access entries reaches every provider.
func (t Account) AllowsProvider(provider string) bool {
	if len(t.Access) == 0 {
		return true
	}
	for _, entry := range t.Access {
		if entry.Provider == provider {
			return true
		}
	}
	return false
}

// AllowsModel reports whether this account may reach the named model on the
// named provider. An access entry without models grants every model that
// provider serves.
func (t Account) AllowsModel(provider, model string) bool {
	if len(t.Access) == 0 {
		return true
	}
	for _, entry := range t.Access {
		if entry.Provider != provider {
			continue
		}
		if len(entry.Models) == 0 {
			return true
		}
		for _, allowed := range entry.Models {
			if allowed == model {
				return true
			}
		}
		return false
	}
	return false
}

// IsDefault reports whether this is the canonical account.
func (t Account) IsDefault() bool { return t.ID == DefaultID }

// OutstandingJobsBound resolves how many jobs this account may hold open,
// treating an unset limit as the default rather than as unlimited.
func (t Account) OutstandingJobsBound() int64 {
	if t.Limits == nil || t.Limits.OutstandingJobs == nil {
		return DefaultOutstandingJobs
	}
	return *t.Limits.OutstandingJobs
}

// EffectiveCredentialStrategy resolves the stored value, treating an unset
// strategy as the default rather than as an error.
func (t Account) EffectiveCredentialStrategy() CredentialStrategy {
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
