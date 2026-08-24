package authmode

import "sync/atomic"

// Policy is the mode the gateway is running under right now.
//
// It exists because the console can change the mode without a restart, and
// every request has to see the change. Reading a value that was captured when
// the router was built would make "disabled" mean "disabled at boot", so the
// authentication middleware reads the policy per request instead.
//
// The zero value and a nil pointer both report Required. A server that never
// bound a policy authenticates, which is the direction this has to fail in.
type Policy struct {
	current atomic.Pointer[Setting]
}

// NewPolicy returns a policy running under setting.
func NewPolicy(setting Setting) *Policy {
	policy := &Policy{}
	policy.Set(setting)
	return policy
}

// Current returns the setting every request is judged against.
func (p *Policy) Current() Setting {
	if p == nil {
		return Setting{Mode: Required, Source: SourceDefault}
	}
	if setting := p.current.Load(); setting != nil {
		return *setting
	}
	return Setting{Mode: Required, Source: SourceDefault}
}

// Set replaces the running setting. It takes effect on the next request.
func (p *Policy) Set(setting Setting) {
	if p == nil {
		return
	}
	resolved := setting.Effective()
	p.current.Store(&resolved)
}

// Disabled reports whether a request may proceed without a gateway API key.
func (p *Policy) Disabled() bool {
	return p.Current().Mode == Disabled
}
