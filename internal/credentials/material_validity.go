package credentials

import (
	"errors"
	"sync/atomic"
	"time"
)

// ErrMaterialExpired reports material outside its allowed validity period.
var ErrMaterialExpired = errors.New("credential material expired")

// MaterialValidity binds a fixed deadline to a shared revocation fence.
// Renewal does not extend the deadline of an existing request handle.
type MaterialValidity struct {
	deadline time.Time
	revoked  *atomic.Bool
}

// NewMaterialValidity starts a revocable credential generation.
func NewMaterialValidity(deadline time.Time) *MaterialValidity {
	return &MaterialValidity{deadline: deadline, revoked: new(atomic.Bool)}
}

// Renew creates a new deadline under the same revocation fence.
func (v *MaterialValidity) Renew(deadline time.Time) *MaterialValidity {
	if v == nil {
		return &MaterialValidity{deadline: deadline}
	}
	return &MaterialValidity{deadline: deadline, revoked: v.revoked}
}

// Revoke invalidates all handles in the credential generation.
func (v *MaterialValidity) Revoke() {
	if v != nil && v.revoked != nil {
		v.revoked.Store(true)
	}
}

// Check rejects revoked or expired material without reading external state.
func (v *MaterialValidity) Check(now time.Time) error {
	if v == nil {
		return nil
	}
	if v.revoked == nil || v.revoked.Load() {
		return ErrMaterialRevoked
	}
	if !now.Before(v.deadline) {
		return ErrMaterialExpired
	}
	return nil
}

// WithValidity binds material to a request-validity handle.
func (m Material) WithValidity(validity *MaterialValidity) Material {
	m.validity = validity
	return m
}

// Validity returns the material's revocation and deadline handle.
func (m Material) Validity() *MaterialValidity { return m.validity }

// CheckValidity checks both the source expiry and the managed deadline.
func (m Material) CheckValidity(now time.Time) error {
	if expiry, ok := m.ExpiresAt(); ok && !now.Before(expiry) {
		return ErrMaterialExpired
	}
	return m.validity.Check(now)
}
