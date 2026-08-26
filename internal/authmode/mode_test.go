package authmode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolvePrecedence is the whole reason a stored mode is safe to have. An
// operator has three ways to state the mode and only one of them survives a
// restart on its own, so what wins has to be decided once and stay decided.
func TestResolvePrecedence(t *testing.T) {
	tests := []struct {
		name       string
		stated     Mode
		source     Source
		persisted  Setting
		wantMode   Mode
		wantSource Source
	}{
		{
			name:       "nothing stated and nothing stored is required",
			wantMode:   Required,
			wantSource: SourceDefault,
		},
		{
			name:       "the stored mode applies when nobody stated one",
			persisted:  Setting{Mode: Disabled, Source: SourceConsole},
			wantMode:   Disabled,
			wantSource: SourceConsole,
		},
		{
			// A stored value that overrode this would turn the deployment's own
			// statement into a suggestion.
			name:       "configuration beats a stored mode",
			stated:     Required,
			source:     SourceConfig,
			persisted:  Setting{Mode: Disabled, Source: SourceConsole},
			wantMode:   Required,
			wantSource: SourceConfig,
		},
		{
			// The inverse matters more: a stored "required" beating --no-auth
			// would strand an operator outside a gateway they cannot reach to
			// change.
			name:       "a flag beats a stored mode",
			stated:     Disabled,
			source:     SourceFlag,
			persisted:  Setting{Mode: Required, Source: SourceConsole},
			wantMode:   Disabled,
			wantSource: SourceFlag,
		},
		{
			name:       "a stated but empty mode is still a statement",
			source:     SourceConfig,
			persisted:  Setting{Mode: Disabled, Source: SourceConsole},
			wantMode:   Required,
			wantSource: SourceConfig,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Resolve(test.stated, test.source, test.persisted)

			assert.Equal(t, test.wantMode, got.Mode)
			assert.Equal(t, test.wantSource, got.Source)
		})
	}
}

// TestAllowsDisabled pins the exposure tripwire at its one owner. Startup
// validation and the runtime switch both call it, so a change here changes
// both, which is the point of it living in one place.
func TestAllowsDisabled(t *testing.T) {
	assert.True(t, AllowsDisabled("127.0.0.1", false))
	assert.True(t, AllowsDisabled("localhost", false))
	assert.False(t, AllowsDisabled("0.0.0.0", false))
	assert.False(t, AllowsDisabled("", false), "an empty host binds every interface")
	assert.True(t, AllowsDisabled("0.0.0.0", true), "a second deliberate act lifts it")
}

func TestLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "127.0.0.1", want: true},
		{host: "127.9.9.9", want: true},
		{host: "::1", want: true},
		{host: "[::1]", want: true},
		{host: "localhost", want: true},
		{host: "LocalHost.", want: true},
		{host: " 127.0.0.1 ", want: true},
		{host: "", want: false},
		{host: "0.0.0.0", want: false},
		{host: "::", want: false},
		{host: "10.0.0.4", want: false},
		// Deciding this by DNS would trust a resolver with a security question
		// and accept an answer that can change after startup.
		{host: "localhost.attacker.example", want: false},
	}
	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			assert.Equal(t, test.want, LoopbackHost(test.host))
		})
	}
}

func TestLoopbackAddr(t *testing.T) {
	assert.True(t, LoopbackAddr("127.0.0.1:54321"))
	assert.True(t, LoopbackAddr("[::1]:54321"))
	assert.True(t, LoopbackAddr("127.0.0.1"))
	assert.False(t, LoopbackAddr("192.0.2.1:1234"), "the httptest default is not this machine")
	assert.False(t, LoopbackAddr(""))
}

func TestLoopbackOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		// curl and every SDK send no origin. Refusing those would make the
		// header a requirement rather than a check.
		{name: "absent", origin: "", want: true},
		{name: "loopback page", origin: "http://127.0.0.1:8080", want: true},
		{name: "localhost page", origin: "http://localhost:5173", want: true},
		{name: "another site", origin: "https://evil.example", want: false},
		// A sandboxed or file:// document sends this. It names no host, so it
		// cannot be shown to be this machine.
		{name: "null", origin: "null", want: false},
		{name: "unparseable", origin: "http://[::", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, LoopbackOrigin(test.origin))
		})
	}
}

// TestPolicyIsReadPerRequest is what makes the switch take effect without a
// restart. A middleware that captured the mode when the router was built would
// make "disabled" mean "disabled at boot".
func TestPolicyIsReadPerRequest(t *testing.T) {
	policy := NewPolicy(Setting{Mode: Required, Source: SourceDefault})
	require.False(t, policy.Disabled())

	policy.Set(Setting{Mode: Disabled, Source: SourceConsole})

	assert.True(t, policy.Disabled())
	assert.Equal(t, SourceConsole, policy.Current().Source)
}

// TestPolicyFailsClosed covers the two ways a policy can be absent. Both have
// to authenticate: a server that never bound a policy is a bug, and the safe
// reading of a bug is the locked one.
func TestPolicyFailsClosed(t *testing.T) {
	var absent *Policy
	assert.False(t, absent.Disabled())
	assert.Equal(t, Required, absent.Current().Mode)

	zero := &Policy{}
	assert.False(t, zero.Disabled())
	assert.Equal(t, Required, zero.Current().Mode)
}

func TestModeValid(t *testing.T) {
	assert.True(t, Mode("").Valid())
	assert.True(t, Required.Valid())
	assert.True(t, Disabled.Valid())
	assert.False(t, Mode("off").Valid())
	assert.False(t, Mode("REQUIRED").Valid(), "the mode is compared exactly, not case-folded")
	assert.Equal(t, Required, Mode("").Effective())
}
