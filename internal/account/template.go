package account

import (
	"errors"
	"time"

	"github.com/agentstation/starport/internal/limits"
)

// Template names the creation defaults an operator stamps onto a new
// account: the limits, the credential strategy, the BYOK policy, and the
// provider access. A template is copied at creation and never consulted
// again, so editing one never rewrites an account it already stamped.
type Template struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Limits is the account-wide cap a stamped account starts with. A nil
	// value stamps no cap, exactly as creating the account by hand would.
	Limits *limits.Limits `json:"limits,omitempty"`
	// CredentialStrategy is the default credential policy a stamped account
	// starts with. An empty value stamps nothing, and the account reads as
	// StrategyOperatorFirst the way every account does.
	CredentialStrategy CredentialStrategy `json:"credential_strategy,omitempty"`
	// BYOKPolicy and Access carry the same contract they carry on an
	// account: nil allows every provider, and an access entry without
	// models grants every model its provider serves.
	BYOKPolicy *BYOKPolicy      `json:"byok_policy,omitempty"`
	Access     []ProviderAccess `json:"access,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

var (
	// ErrTemplateNotFound reports a missing account template.
	ErrTemplateNotFound = errors.New("account template not found")
	// ErrTemplateConflict reports an existing template or a stale revision.
	ErrTemplateConflict = errors.New("account template revision conflict")
	// ErrCorruptTemplate reports invalid durable template data.
	ErrCorruptTemplate = errors.New("account template record is invalid")
)

// Validate checks the template invariants. The policy fields carry the
// account contract, because a template that cannot become a valid account
// is a template that can never be used.
func (t Template) Validate() error {
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

// Stamp copies the template's creation defaults onto an account. It copies
// clones, so the stamped account and the template never share storage, and
// an edit to either can never move the other.
func (t Template) Stamp(target *Account) {
	if target == nil {
		return
	}
	target.Limits = t.Limits.Clone()
	target.CredentialStrategy = t.CredentialStrategy
	target.BYOKPolicy = cloneBYOKPolicy(t.BYOKPolicy)
	target.Access = cloneProviderAccess(t.Access)
}

func cloneTemplate(value Template) Template {
	clone := value
	clone.Limits = value.Limits.Clone()
	clone.BYOKPolicy = cloneBYOKPolicy(value.BYOKPolicy)
	clone.Access = cloneProviderAccess(value.Access)
	return clone
}
