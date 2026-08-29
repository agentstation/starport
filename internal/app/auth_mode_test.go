package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
)

// AON6 guarded a mirror here: the configuration package and the HTTP seam each
// spelled the mode, and a test proved the two spellings matched. AON7 removed
// the mirror instead of guarding it, because the runtime switch needs the mode
// type, the bind host, and the acknowledgment in one place, and a rule split
// across two packages is a rule with two versions. internal/authmode owns all
// of it now, and there is nothing left for a drift test to compare.

// TestServerConfigCarriesTheResolvedDecision proves what composition hands the
// HTTP seam. The seam never decides what an empty mode means, so the unset
// case is the one that matters: an operator who states nothing gets the safe
// mode, and it is composition that says so.
func TestServerConfigCarriesTheResolvedDecision(t *testing.T) {
	tests := []struct {
		name       string
		setting    authmode.Setting
		wantMode   authmode.Mode
		wantSource authmode.Source
	}{
		{name: "unset", setting: authmode.Setting{}, wantMode: authmode.Required, wantSource: authmode.SourceDefault},
		{
			name:       "required by config",
			setting:    authmode.Setting{Mode: authmode.Required, Source: authmode.SourceConfig},
			wantMode:   authmode.Required,
			wantSource: authmode.SourceConfig,
		},
		{
			name:       "disabled by flag",
			setting:    authmode.Setting{Mode: authmode.Disabled, Source: authmode.SourceFlag},
			wantMode:   authmode.Disabled,
			wantSource: authmode.SourceFlag,
		},
		{
			name:       "disabled by the console",
			setting:    authmode.Setting{Mode: authmode.Disabled, Source: authmode.SourceConsole},
			wantMode:   authmode.Disabled,
			wantSource: authmode.SourceConsole,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Security.UnauthenticatedScopes = []string{"chat:write"}

			resolved := serverConfig(cfg, authRuntime{setting: test.setting})

			require.NotNil(t, resolved)
			assert.Equal(t, test.wantMode, resolved.AuthMode)
			assert.Equal(t, test.wantSource, resolved.AuthModeSource)
			assert.Equal(t, []string{"chat:write"}, resolved.UnauthenticatedScopes)
		})
	}
}

// TestAuthModeSourceNamesWhatStatedIt pins the distinction the console depends
// on. The switch refuses to change a mode an operator stated for this process,
// and it can only refuse if the three sources that write one field stay
// distinguishable after they have written it.
func TestAuthModeSourceNamesWhatStatedIt(t *testing.T) {
	tests := []struct {
		name  string
		build func() *config.Config
		want  authmode.Source
	}{
		{
			name:  "nobody stated a mode",
			build: func() *config.Config { return &config.Config{} },
			want:  authmode.SourceUnset,
		},
		{
			name: "the environment stated it",
			build: func() *config.Config {
				cfg := &config.Config{}
				cfg.Security.AuthMode = config.AuthModeRequired
				return cfg
			},
			want: authmode.SourceConfig,
		},
		{
			name: "a flag stated it",
			build: func() *config.Config {
				cfg := &config.Config{}
				config.DisableAuthentication()(cfg)
				return cfg
			},
			want: authmode.SourceFlag,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.build().AuthModeSource())
		})
	}
}

// TestRequireIdentityJudgesTheResolvedMode is the ordering bug this task could
// have shipped. A gateway that stored "disabled" from the console holds no
// key on purpose, and a startup check that read the raw configuration value
// would refuse to start it.
func TestRequireIdentityJudgesTheResolvedMode(t *testing.T) {
	resolved := authmode.Resolve("", authmode.SourceUnset, authmode.Setting{
		Mode:   authmode.Disabled,
		Source: authmode.SourceConsole,
	})

	identities, err := apikey.Open(storage.NewMockStore())
	require.NoError(t, err)

	require.Equal(t, authmode.Disabled, resolved.Mode)
	require.NoError(t, requireIdentity(t.Context(), identities, resolved.Mode))
	require.ErrorIs(t, requireIdentity(t.Context(), identities, authmode.Required), ErrIdentityRequired)
}
