// Package authmode owns whether the gateway requires a gateway API key.
//
// The mode is one decision with three ways to state it — a configuration
// value, a command-line flag, and a console switch — and one rule about when
// it is safe to disable. Every one of those needs the same vocabulary, so the
// vocabulary lives here rather than being spelled once per package: a mode a
// reader fails to recognize is merely wrong, but a mode it recognizes when it
// should not is an open gateway.
package authmode

import (
	"net"
	"net/url"
	"strings"
	"time"
)

// Mode selects whether a request must carry a gateway API key.
type Mode string

const (
	// Required refuses every request that carries no valid gateway API key.
	Required Mode = "required"
	// Disabled serves every request without checking for a key. The gateway
	// still meters, governs, and attributes the request; it just has no
	// caller-supplied name for it.
	Disabled Mode = "disabled"
)

// Effective returns the mode the gateway runs under. An unset value is
// Required: the state an operator reaches by not deciding has to be the safe
// one.
func (m Mode) Effective() Mode {
	if m == "" {
		return Required
	}
	return m
}

// Valid reports whether the mode names a state the gateway can run in. An
// unset value is valid and means Required.
func (m Mode) Valid() bool {
	switch m {
	case "", Required, Disabled:
		return true
	default:
		return false
	}
}

// Source names where the running mode came from. An operator who wants to
// change the mode has to change the thing that set it, and the four sources
// are changed in four different places.
type Source string

const (
	// SourceUnset means nobody stated a mode. It is the input to Resolve and
	// never the answer it returns.
	SourceUnset Source = ""
	// SourceDefault means no configuration, flag, or stored value existed.
	SourceDefault Source = "default"
	// SourceConfig means a configuration value or environment variable stated it.
	SourceConfig Source = "config"
	// SourceFlag means a command-line flag stated it for this process only.
	SourceFlag Source = "flag"
	// SourceConsole means an operator changed it at runtime and it was stored.
	SourceConsole Source = "console"
)

// Setting is one stated mode and the place it was stated.
type Setting struct {
	Mode      Mode      `json:"mode"`
	Source    Source    `json:"source"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Effective resolves the unset cases so a reader never has to.
func (s Setting) Effective() Setting {
	s.Mode = s.Mode.Effective()
	if s.Source == SourceUnset {
		s.Source = SourceDefault
	}
	return s
}

// Resolve returns the setting the gateway starts under.
//
// A configuration value or a flag is the operator speaking about this process,
// and it wins: a stored value that silently overrode an explicit
// STARPORT_SECURITY_AUTH_MODE=required would turn a deployment's own statement
// into a suggestion, and a stored value that overrode --no-auth would leave an
// operator with no way to reach a gateway whose stored mode they cannot log in
// to change. The stored value applies exactly when nobody stated anything,
// which is the case a console change exists to serve.
func Resolve(stated Mode, source Source, persisted Setting) Setting {
	if source != SourceUnset {
		return Setting{Mode: stated.Effective(), Source: source}
	}
	if persisted.Mode != "" {
		return persisted.Effective()
	}
	return Setting{Mode: Required, Source: SourceDefault}
}

// AllowsDisabled reports whether authentication may be off for a gateway bound
// to bindHost.
//
// An unauthenticated gateway on a reachable address is an open inference
// endpoint, and the bind address is the only evidence anything has about who
// can reach it. Turning it off there takes two deliberate acts, so the
// acknowledgment is a separate argument rather than a wider reading of the
// address.
//
// This is the one place the rule lives. Startup validation and the runtime
// switch both call it, because a rule enforced at startup and restated at
// runtime is a rule with two versions.
func AllowsDisabled(bindHost string, acknowledged bool) bool {
	return acknowledged || LoopbackHost(bindHost)
}

// LoopbackHost reports whether a bind address reaches only this machine.
//
// An empty host is not loopback: an empty address binds every interface, which
// is the exposure the caller is asking about. A name other than localhost is
// not loopback either, because deciding otherwise would need a DNS lookup
// whose answer can change after startup, and a resolver is the wrong thing to
// trust with this question.
func LoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// LoopbackAddr reports whether an address in host:port form, such as an
// http.Request RemoteAddr, reaches only this machine. An address without a
// port is read as a bare host.
func LoopbackAddr(addr string) bool {
	if host, _, err := net.SplitHostPort(strings.TrimSpace(addr)); err == nil {
		return LoopbackHost(host)
	}
	return LoopbackHost(addr)
}

// LoopbackOrigin reports whether a browser Origin header names this machine.
//
// An absent origin is loopback: a request from curl or an SDK carries none,
// and refusing those would make the header a requirement rather than a check.
// What the check catches is the origin that is present and names somewhere
// else, which is a page on another site driving a browser that can reach the
// gateway.
func LoopbackOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	// "null" is what a sandboxed or file:// document sends. It names no host,
	// so it cannot be shown to be this machine.
	if strings.EqualFold(origin, "null") {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return LoopbackHost(parsed.Hostname())
}
