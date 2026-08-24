package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
)

// TestAuthModeVocabularyMatchesAcrossSeams guards a mirror. The configuration
// package owns the operator's word for the mode and the HTTP seam carries the
// resolved string, because the HTTP seam does not read operator
// configuration. Two spellings of the same decision can drift silently, and
// the drift fails open in the worst case: a mode the server does not
// recognize is not "disabled", but a mode it recognizes when it should not is
// an open gateway.
func TestAuthModeVocabularyMatchesAcrossSeams(t *testing.T) {
	assert.Equal(t, server.AuthModeRequired, string(config.AuthModeRequired))
	assert.Equal(t, server.AuthModeDisabled, string(config.AuthModeDisabled))
}

// TestServerConfigCarriesTheAuthenticationDecision proves the mapping the
// mirror above depends on, including the unset case: composition resolves the
// mode so the HTTP seam never has to decide what an empty value means.
func TestServerConfigCarriesTheAuthenticationDecision(t *testing.T) {
	tests := []struct {
		name string
		mode config.AuthMode
		want string
	}{
		{name: "unset", mode: "", want: server.AuthModeRequired},
		{name: "required", mode: config.AuthModeRequired, want: server.AuthModeRequired},
		{name: "disabled", mode: config.AuthModeDisabled, want: server.AuthModeDisabled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Security.AuthMode = test.mode
			cfg.Security.UnauthenticatedScopes = []string{"chat:write"}

			resolved := serverConfig(cfg)

			require.NotNil(t, resolved)
			assert.Equal(t, test.want, resolved.AuthMode)
			assert.Equal(t, []string{"chat:write"}, resolved.UnauthenticatedScopes)
		})
	}
}
