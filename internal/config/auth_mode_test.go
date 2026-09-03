package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthModeDefaultsToRequired pins the direction the unset value falls in.
// Everything else in this file depends on it: a zero value that meant
// "disabled" would open every deployment that never set the variable.
func TestAuthModeDefaultsToRequired(t *testing.T) {
	assert.Equal(t, AuthModeRequired, AuthMode("").Effective())
	assert.Equal(t, AuthModeDisabled, AuthModeDisabled.Effective())
}

func TestSecurityConfigRejectsAnUnknownAuthMode(t *testing.T) {
	security := SecurityConfig{AuthMode: "optional", AllowedOrigins: "*"}

	err := security.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid auth mode")
}

// TestAuthenticationExposureTripwire is the AON6 acceptance matrix. The
// refusal exists because an unauthenticated gateway on a reachable address is
// an open inference endpoint, and the bind address is the only evidence
// startup has about who can reach it.
func TestAuthenticationExposureTripwire(t *testing.T) {
	tests := []struct {
		name        string
		mode        AuthMode
		host        string
		allowRemote bool
		wantErr     bool
	}{
		{name: "required on a reachable address", mode: AuthModeRequired, host: "0.0.0.0"},
		{name: "disabled on loopback IPv4", mode: AuthModeDisabled, host: "127.0.0.1"},
		{name: "disabled on loopback by name", mode: AuthModeDisabled, host: "localhost"},
		{name: "disabled on loopback IPv6", mode: AuthModeDisabled, host: "::1"},
		{name: "disabled on every interface", mode: AuthModeDisabled, host: "0.0.0.0", wantErr: true},
		{name: "disabled on a routable address", mode: AuthModeDisabled, host: "10.0.0.5", wantErr: true},
		// An empty host binds every interface, so it is the exposure the
		// tripwire exists to catch and not a missing answer.
		{name: "disabled on an empty host", mode: AuthModeDisabled, host: "", wantErr: true},
		{
			name: "disabled on every interface with the acknowledgment",
			mode: AuthModeDisabled, host: "0.0.0.0", allowRemote: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{
				Server:   ServerConfig{Host: test.host},
				Security: SecurityConfig{AuthMode: test.mode, AllowRemoteNoAuth: test.allowRemote},
			}

			err := cfg.validateAuthenticationExposure()

			if !test.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			// The message has to name the way out, or an operator who hits it
			// has a refusal and no next step.
			assert.Contains(t, err.Error(), "--allow-remote-no-auth")
			assert.Contains(t, err.Error(), "STARPORT_SECURITY_ALLOW_REMOTE_NO_AUTH")
		})
	}
}

// TestDisableAuthenticationOverrideMeetsValidation is why Override exists. A
// flag that skipped validation would leave the tripwire guarding the
// environment variable only, which is the path an operator is least likely to
// take when opening a gateway for a quick test.
func TestDisableAuthenticationOverrideMeetsValidation(t *testing.T) {
	cfg := validExposureConfig()
	cfg.Server.Host = "0.0.0.0"
	DisableAuthentication()(cfg)

	require.Error(t, cfg.Validate())

	AllowRemoteWithoutAuthentication()(cfg)
	require.NoError(t, cfg.Validate())
}

// validExposureConfig is a configuration that validates, so a failure in the
// tests above is the tripwire and never an unrelated invalid field.
func validExposureConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 8080, Host: "127.0.0.1",
			ReadTimeout: 30_000_000_000, WriteTimeout: 30_000_000_000,
			IdleTimeout: 120_000_000_000, MaxHeaderBytes: 1 << 20,
		},
		Storage:      StorageConfig{Mode: storageModeBadger, Badger: BadgerConfig{Path: "/tmp/starport-test", GCInterval: 300_000_000_000, GCDiscardRatio: 0.5}, SQL: SQLConfig{Mode: sqlModeSQLite}},
		RateLimiting: RateLimitingConfig{DefaultRequestsPerMinute: 60, WindowSize: 60_000_000_000},
		Security:     SecurityConfig{AllowedOrigins: "*"},
		Logging:      LoggingConfig{Level: "info", Format: "json", Output: "stdout", MaxSize: 100},
		Catalog:      DefaultCatalogConfig(),
	}
}
