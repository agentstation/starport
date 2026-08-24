package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/localauth"
)

// authDependencies points the auth commands at a temporary machine, so a test
// run can mint and rotate a credential without touching the operator's own.
func authDependencies(t *testing.T) (Dependencies, *bytes.Buffer, config.Paths) {
	t.Helper()
	deps, stdout, _ := testDependencies()
	paths := config.PathsForConfigDir(t.TempDir())
	deps.ResolvePaths = func() (config.Paths, error) { return paths, nil }
	return deps, stdout, paths
}

func runAuth(t *testing.T, deps Dependencies, args ...string) error {
	t.Helper()
	return Run(context.Background(), append([]string{"starport", "auth"}, args...), deps)
}

// TestAuthTokenPrintsOnlyTheSecret keeps the command composable. An operator
// pipes it into a clipboard or a curl header, and a paragraph of explanation in
// that pipe becomes part of the credential.
func TestAuthTokenPrintsOnlyTheSecret(t *testing.T) {
	deps, output, paths := authDependencies(t)

	require.NoError(t, runAuth(t, deps, "token"))

	printed := strings.TrimRight(output.String(), "\n")
	assert.NotContains(t, printed, "\n", "the text output is one line")
	assert.True(t, strings.HasPrefix(printed, localauth.TokenPrefix), "printed %q", printed)

	// What the command printed has to be what a gateway reading the same file
	// would accept, or the operator is holding a value nothing honours.
	store, err := localauth.NewStore(paths.LocalTokenFile)
	require.NoError(t, err)
	stored, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.True(t, stored.Authorizes(printed))
}

// TestAuthTokenIsStableAcrossCalls covers the operator who runs it twice. A
// command that minted on every call would hand out a new credential each time
// and invalidate the one already pasted somewhere.
func TestAuthTokenIsStableAcrossCalls(t *testing.T) {
	deps, output, _ := authDependencies(t)

	require.NoError(t, runAuth(t, deps, "token"))
	first := output.String()
	output.Reset()
	require.NoError(t, runAuth(t, deps, "token"))

	assert.Equal(t, first, output.String())
}

// TestAuthStatusOnAColdMachineIsNotAFailure matters because status is the first
// command a confused operator runs. "Nothing here yet" is an answer; a non-zero
// exit is a second problem to debug.
func TestAuthStatusOnAColdMachineIsNotAFailure(t *testing.T) {
	deps, output, paths := authDependencies(t)

	require.NoError(t, runAuth(t, deps, "status"))

	assert.Contains(t, output.String(), "none yet")
	assert.Contains(t, output.String(), paths.LocalTokenFile)
}

// TestAuthStatusNeverPrintsTheSecret is the point of having a separate status
// command at all. It is the one an operator runs while someone is watching.
func TestAuthStatusNeverPrintsTheSecret(t *testing.T) {
	deps, output, paths := authDependencies(t)
	require.NoError(t, runAuth(t, deps, "token"))
	secret := strings.TrimSpace(output.String())
	require.NotEmpty(t, secret)

	for _, args := range [][]string{{"status"}, {"status", "--json"}} {
		output.Reset()
		require.NoError(t, runAuth(t, deps, args...))
		assert.NotContains(t, output.String(), secret, "`auth %s` leaked the token", strings.Join(args, " "))
		assert.Contains(t, output.String(), paths.LocalTokenFile)
	}
}

// TestAuthStatusReportsTheExposureAnswer is why an operator runs the command
// before binding a public address: they want to know whether the gateway will
// start, and the answer has to name the command that changes it.
func TestAuthStatusReportsTheExposureAnswer(t *testing.T) {
	deps, output, _ := authDependencies(t)
	require.NoError(t, runAuth(t, deps, "token"))
	output.Reset()

	require.NoError(t, runAuth(t, deps, "status", "--json"))
	var before authStatusView
	require.NoError(t, json.Unmarshal([]byte(output.String()), &before))
	assert.True(t, before.Present)
	assert.Equal(t, uint64(1), before.Generation)
	assert.False(t, before.AllowsNetworkBind)
	assert.Nil(t, before.RotatedAt)

	output.Reset()
	require.NoError(t, runAuth(t, deps, "status"))
	assert.Contains(t, output.String(), localauth.RotateCommand)

	output.Reset()
	require.NoError(t, runAuth(t, deps, "rotate"))
	output.Reset()

	require.NoError(t, runAuth(t, deps, "status", "--json"))
	var after authStatusView
	require.NoError(t, json.Unmarshal([]byte(output.String()), &after))
	assert.Equal(t, uint64(2), after.Generation)
	assert.True(t, after.AllowsNetworkBind)
	require.NotNil(t, after.RotatedAt)
}

// TestAuthRotateSaysWhatDidNotChange covers the support question the command
// would otherwise create. A gateway that is already running keeps the token it
// read at startup, and an operator who does not know that concludes the new
// token is broken.
func TestAuthRotateSaysWhatDidNotChange(t *testing.T) {
	deps, output, _ := authDependencies(t)
	require.NoError(t, runAuth(t, deps, "token"))
	first := strings.TrimSpace(output.String())
	output.Reset()

	require.NoError(t, runAuth(t, deps, "rotate"))

	rotated := output.String()
	assert.Contains(t, rotated, "generation 2")
	assert.NotContains(t, rotated, first, "a rotation replaces the secret")
	assert.Contains(t, rotated, "Restart it")
}

// TestAuthRotateWorksOnAColdMachine follows the instruction the startup refusal
// gives. An operator told to rotate before they have ever started the gateway
// has to get a token, not an error telling them to start it first.
func TestAuthRotateWorksOnAColdMachine(t *testing.T) {
	deps, output, _ := authDependencies(t)

	require.NoError(t, runAuth(t, deps, "rotate"))

	assert.Contains(t, output.String(), "generation 1")
	assert.Contains(t, output.String(), localauth.TokenPrefix)
}

// TestAuthNamesItsSubcommands keeps the group discoverable. `starport auth` on
// its own is what an operator types when they do not know what is here.
func TestAuthNamesItsSubcommands(t *testing.T) {
	deps, output, _ := authDependencies(t)

	require.NoError(t, runAuth(t, deps))

	for _, name := range []string{"token", "status", "rotate"} {
		assert.Contains(t, output.String(), name)
	}
}

func TestAuthRejectsAnUnknownSubcommand(t *testing.T) {
	deps, _, _ := authDependencies(t)

	err := runAuth(t, deps, "revoke")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "revoke")
}
