package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/localauth"
	"github.com/stretchr/testify/require"
)

func TestAuthRotateMetadataExcludesSecretsAndInvalidatesOldSessions(t *testing.T) {
	for _, structured := range []bool{false, true} {
		name := "text"
		if structured {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			deps, output, paths := authDependencies(t)
			store, err := localauth.NewStore(paths.LocalTokenFile)
			require.NoError(t, err)
			old, _, err := store.LoadOrMint(t.Context(), time.Now())
			require.NoError(t, err)
			cookie, _, err := localauth.IssueSession(old, localauth.GrantLocalToken, time.Now())
			require.NoError(t, err)
			args := []string{"rotate", "--no-secret"}
			if structured {
				args = append(args, "--json")
			}
			require.NoError(t, runAuth(t, deps, args...))
			current, err := store.Peek(t.Context())
			require.NoError(t, err)
			require.Equal(t, old.Generation+1, current.Generation)
			require.False(t, current.Authorizes(old.Secret))
			_, err = localauth.VerifySession(cookie, current, time.Now())
			require.Error(t, err)
			require.NotContains(t, output.String(), old.Secret)
			require.NotContains(t, output.String(), current.Secret)
			require.NotContains(t, output.String(), localauth.TokenPrefix)
			if structured {
				var result map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(output.Bytes(), &result))
				require.NotContains(t, result, "secret")
				require.JSONEq(t, "true", string(result["restart_required"]))
				var path string
				require.NoError(t, json.Unmarshal(result["token_file"], &path))
				require.Equal(t, paths.LocalTokenFile, path)
			} else {
				require.Contains(t, output.String(), paths.LocalTokenFile)
				require.Contains(t, strings.ToLower(output.String()), "restart")
			}
		})
	}
}

func TestAuthRotateMetadataCanCreateTargetCredential(t *testing.T) {
	deps, output, paths := authDependencies(t)
	require.NoError(t, runAuth(t, deps, "rotate", "--no-secret", "--json"))
	store, err := localauth.NewStore(paths.LocalTokenFile)
	require.NoError(t, err)
	current, err := store.Peek(t.Context())
	require.NoError(t, err)
	require.True(t, current.Rotated())
	require.NotContains(t, output.String(), current.Secret)
}
