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

// TestAuthTokenCopiesInsteadOfPrinting is the whole reason the flag exists. An
// operator pastes this token into a browser once and then lives with it in the
// scrollback; --copy is the path that keeps it out of there, and a copy that
// also printed would defeat the flag entirely.
func TestAuthTokenCopiesInsteadOfPrinting(t *testing.T) {
	deps, output, paths := authDependencies(t)
	var copied []string
	deps.Desktop.CopyToClipboard = func(_ context.Context, text string) error {
		copied = append(copied, text)
		return nil
	}

	require.NoError(t, runAuth(t, deps, "token", "--copy"))

	require.Len(t, copied, 1)
	assert.NotContains(t, output.String(), copied[0], "--copy printed the secret it was asked to hide")
	assert.Contains(t, output.String(), "Copied the local admin token to the clipboard.")

	// What reached the clipboard has to be what a gateway reading the same file
	// would accept. A flag that copies a value nothing honours is worse than no
	// flag, because the failure surfaces in a browser rather than here.
	store, err := localauth.NewStore(paths.LocalTokenFile)
	require.NoError(t, err)
	stored, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.True(t, stored.Authorizes(copied[0]))
}

// TestAuthTokenWithoutCopyReachesNoClipboard is the other half: the default is
// still a printed secret, and nothing leaves the process without being asked.
func TestAuthTokenWithoutCopyReachesNoClipboard(t *testing.T) {
	deps, _, _ := authDependencies(t)
	deps.Desktop.CopyToClipboard = func(context.Context, string) error {
		t.Error("the clipboard was written without --copy")
		return nil
	}

	require.NoError(t, runAuth(t, deps, "token"))
}

// TestAuthTokenCopyFallsBackToPrintingAndStillFails covers the machine with no
// clipboard command — a container, a host reached over SSH. The operator must
// end up holding the token either way, and the exit status must still say the
// copy did not happen so a script does not carry on as though it had.
func TestAuthTokenCopyFallsBackToPrintingAndStillFails(t *testing.T) {
	deps, output, paths := authDependencies(t)
	deps.Desktop.CopyToClipboard = func(context.Context, string) error { return ErrNoClipboard }

	err := runAuth(t, deps, "token", "--copy")

	require.Error(t, err)
	assert.Equal(t, ExitCodeRuntime, ExitCode(err))
	assert.ErrorIs(t, err, ErrNoClipboard)

	store, storeErr := localauth.NewStore(paths.LocalTokenFile)
	require.NoError(t, storeErr)
	stored, storeErr := store.Load(context.Background())
	require.NoError(t, storeErr)
	assert.Contains(t, output.String(), stored.Secret, "--copy swallowed the token it could not copy")
}

// TestAuthTokenRefusesCopyWithJSON keeps the two output modes apart. --json is
// read by a program and --copy is read by a person, and the combination would
// leave a JSON document holding the secret on the clipboard.
func TestAuthTokenRefusesCopyWithJSON(t *testing.T) {
	deps, output, _ := authDependencies(t)
	deps.Desktop.CopyToClipboard = func(context.Context, string) error {
		t.Error("a refused invocation still reached the clipboard")
		return nil
	}

	err := runAuth(t, deps, "token", "--copy", "--json")

	require.Error(t, err)
	assert.Equal(t, ExitCodeUsage, ExitCode(err))
	assert.Contains(t, err.Error(), "--copy with --json")
	assert.NotContains(t, output.String(), localauth.TokenPrefix, "a refused invocation printed the secret")
}

// TestAuthHelpNamesTheDesktopVerbs is what the first-contact page depends on.
// The page tells a reader to run a command; a flag that works but is invisible
// in help is a flag nobody finds.
//
// The assertion is anchored rather than a substring, because a substring match
// on "--copy" is also satisfied by "--copy-anything": it would report a renamed
// flag as a present one, which is the exact mistake this test exists to catch.
func TestAuthHelpNamesTheDesktopVerbs(t *testing.T) {
	for _, testCase := range []struct {
		args  []string
		flags []string
	}{
		{args: []string{"token", "--help"}, flags: []string{"copy"}},
		{args: []string{"url", "--help"}, flags: []string{"copy", "open"}},
	} {
		t.Run(testCase.args[0], func(t *testing.T) {
			deps, output, _ := authDependencies(t)

			require.NoError(t, runAuth(t, deps, testCase.args...))

			for _, flag := range testCase.flags {
				assert.Regexp(t, `(?m)^\s+--`+flag+`[\s,]`, output.String())
			}
		})
	}
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
	}

	// The file is named in both forms, but only the text form names it
	// literally: JSON escapes a Windows path's separators, so a substring
	// assertion there would be asserting the encoding rather than the path.
	output.Reset()
	require.NoError(t, runAuth(t, deps, "status"))
	assert.Contains(t, output.String(), paths.LocalTokenFile)

	output.Reset()
	require.NoError(t, runAuth(t, deps, "status", "--json"))
	var decoded authStatusView
	require.NoError(t, json.Unmarshal([]byte(output.String()), &decoded))
	assert.Equal(t, paths.LocalTokenFile, decoded.TokenFile)
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
