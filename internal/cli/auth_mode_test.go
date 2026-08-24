package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoAuthFlagsReachTheServerRunner pins the wiring the whole flag depends
// on. A flag that parses but never reaches the runtime is the failure mode
// that looks like success in every manual test an operator would run.
func TestNoAuthFlagsReachTheServerRunner(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want GatewayOptions
	}{
		{name: "default", args: []string{"starport", "serve"}},
		{
			name: "no auth",
			args: []string{"starport", "serve", "--no-auth"},
			want: GatewayOptions{DisableAuth: true},
		},
		{
			name: "no auth on a reachable address",
			args: []string{"starport", "serve", "--no-auth", "--allow-remote-no-auth"},
			want: GatewayOptions{DisableAuth: true, AllowRemoteNoAuth: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps, _, _ := testDependencies()
			var received GatewayOptions
			deps.RunServer = func(_ context.Context, options GatewayOptions) error {
				received = options
				return nil
			}

			require.NoError(t, Run(context.Background(), test.args, deps))
			assert.Equal(t, test.want, received)
		})
	}
}

// TestDevNoAuthFlagReachesTheStarter covers the other command. The
// development gateway binds loopback and nothing else, so it takes --no-auth
// and deliberately offers no remote acknowledgment.
func TestDevNoAuthFlagReachesTheStarter(t *testing.T) {
	deps, stdout, _ := testDependencies()
	var received GatewayOptions
	deps.StartDevelopment = func(_ context.Context, options GatewayOptions) (DevelopmentSession, error) {
		received = options
		return DevelopmentSession{
			URL: "http://127.0.0.1:8080", AuthDisabled: options.DisableAuth,
			Run:   func(context.Context) error { return nil },
			Close: func(context.Context) error { return nil },
		}, nil
	}

	require.NoError(t, Run(context.Background(), []string{"starport", "dev", "--no-auth"}, deps))

	assert.True(t, received.DisableAuth)
	// The banner has to say the mode, because an operator who sees no key
	// printed otherwise cannot tell a disabled gateway from a broken one.
	assert.Contains(t, stdout.String(), "Authentication: disabled")
	assert.NotContains(t, stdout.String(), "Gateway API key")
}

// TestDevSessionRejectsAKeyModeMismatch guards an equivalence, not two
// independent checks. A session that requires a key must carry one to print,
// and a session that requires none must not have minted one: either mismatch
// means the runtime and the mode disagree, and the operator is left with no
// way to make the first request.
func TestDevSessionRejectsAKeyModeMismatch(t *testing.T) {
	tests := []struct {
		name    string
		session DevelopmentSession
		wantErr bool
	}{
		{
			name:    "required with a key",
			session: DevelopmentSession{URL: "http://127.0.0.1:8080", APIKey: "k"},
		},
		{
			name:    "disabled without a key",
			session: DevelopmentSession{URL: "http://127.0.0.1:8080", AuthDisabled: true},
		},
		{
			name:    "required without a key",
			session: DevelopmentSession{URL: "http://127.0.0.1:8080"},
			wantErr: true,
		},
		{
			name:    "disabled with a key",
			session: DevelopmentSession{URL: "http://127.0.0.1:8080", APIKey: "k", AuthDisabled: true},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := test.session
			session.Run = func(context.Context) error { return nil }
			session.Close = func(context.Context) error { return nil }

			err := session.validate()

			if test.wantErr {
				require.ErrorIs(t, err, ErrDevelopmentSessionInvalid)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestServeHelpDocumentsTheAuthenticationFlags keeps the flags discoverable.
// An operator looking for the way to try Starport without a key looks here
// first, and a flag that only the source code mentions is not an option.
func TestServeHelpDocumentsTheAuthenticationFlags(t *testing.T) {
	deps, stdout, _ := testDependencies()

	require.NoError(t, Run(context.Background(), []string{"starport", "help", "serve"}, deps))

	help := stdout.String()
	assert.True(t, strings.Contains(help, "--"+flagNoAuth), help)
	assert.True(t, strings.Contains(help, "--"+flagAllowRemoteNoAuth), help)
}
