package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/localauth"
)

// uiDependencies points the launch commands at a temporary machine with a real
// address, so the URL they produce is one a browser could actually open.
func uiDependencies(t *testing.T) (Dependencies, *bytes.Buffer, config.Paths) {
	t.Helper()
	deps, stdout, _ := testDependencies()
	paths := config.PathsForConfigDir(t.TempDir())
	deps.ResolvePaths = func() (config.Paths, error) { return paths, nil }
	deps.LoadConfig = func(context.Context) (*config.Config, error) {
		cfg := &config.Config{}
		cfg.Server.Host = "127.0.0.1"
		cfg.Server.Port = 8080
		cfg.Security.LocalTokenPath = paths.LocalTokenFile
		return cfg, nil
	}
	return deps, stdout, paths
}

func runCLI(t *testing.T, deps Dependencies, args ...string) error {
	t.Helper()
	return Run(context.Background(), append([]string{"starport"}, args...), deps)
}

// launchTicket pulls the ticket out of a printed launch URL.
func launchTicket(t *testing.T, output string) string {
	t.Helper()
	line := strings.TrimSpace(strings.SplitN(output, "\n", 2)[0])
	parsed, err := url.Parse(line)
	require.NoError(t, err, "printed %q", line)
	assert.Equal(t, localauth.LaunchPath, parsed.Path)
	ticket := parsed.Query().Get(localauth.TicketParam)
	require.NotEmpty(t, ticket, "printed %q", line)
	return ticket
}

// TestUIPrintsALinkTheGatewayWillAccept is the whole point of the command: an
// operator runs it, and the link works against the gateway reading the same
// token file. A link the gateway would refuse is worse than no command at all.
func TestUIPrintsALinkTheGatewayWillAccept(t *testing.T) {
	deps, output, paths := uiDependencies(t)
	t.Setenv("NO_BROWSER", "1")

	require.NoError(t, runCLI(t, deps, "ui", "--no-open"))

	ticket := launchTicket(t, output.String())

	store, err := localauth.NewStore(paths.LocalTokenFile)
	require.NoError(t, err)
	token, err := store.Load(context.Background())
	require.NoError(t, err)
	_, _, err = localauth.NewGate(token).Redeem(ticket, time.Now())
	assert.NoError(t, err, "the gateway should accept the link the CLI just printed")
}

// TestUIWorksBeforeTheGatewayHasEverRun covers the first run. The command reads
// a token file and mints one if it is absent, so it does not need a listening
// gateway — which matters most when the gateway is up and not letting anyone in.
func TestUIWorksBeforeTheGatewayHasEverRun(t *testing.T) {
	deps, output, paths := uiDependencies(t)
	t.Setenv("NO_BROWSER", "1")

	require.NoFileExists(t, paths.LocalTokenFile)
	require.NoError(t, runCLI(t, deps, "ui", "--no-open"))

	assert.FileExists(t, paths.LocalTokenFile)
	launchTicket(t, output.String())
}

// TestEachLaunchLinkIsDifferent guards the single-use property from the other
// side. A command that printed the same link twice would give an operator two
// copies of one ticket and a puzzle the second time they used one.
func TestEachLaunchLinkIsDifferent(t *testing.T) {
	deps, output, _ := uiDependencies(t)
	t.Setenv("NO_BROWSER", "1")

	require.NoError(t, runCLI(t, deps, "ui", "--no-open"))
	first := launchTicket(t, output.String())
	output.Reset()
	require.NoError(t, runCLI(t, deps, "ui", "--no-open"))

	assert.NotEqual(t, first, launchTicket(t, output.String()))
}

func TestUIOpensTheLinkItPrinted(t *testing.T) {
	deps, output, _ := uiDependencies(t)
	t.Setenv("CI", "")
	t.Setenv("NO_BROWSER", "")
	var opened []string
	deps.Desktop.OpenBrowser = func(target string) error {
		opened = append(opened, target)
		return nil
	}
	deps.Desktop.IsTerminal = func(io.Writer) bool { return true }

	require.NoError(t, runCLI(t, deps, "ui"))

	printed := strings.TrimSpace(strings.SplitN(output.String(), "\n", 2)[0])
	assert.Equal(t, []string{printed}, opened)
}

// TestUISuppressesTheBrowserWhereThereIsNobodyToSeeIt covers the automated run.
// A ticket works once and expires in a minute and a half, so a browser opened
// where nobody is watching does not merely fail — it burns the link and leaves
// the operator holding a dead one.
func TestUISuppressesTheBrowserWhereThereIsNobodyToSeeIt(t *testing.T) {
	for _, variable := range []string{"CI", "NO_BROWSER"} {
		t.Run(variable, func(t *testing.T) {
			deps, output, _ := uiDependencies(t)
			t.Setenv("CI", "")
			t.Setenv("NO_BROWSER", "")
			t.Setenv(variable, "1")
			opened := 0
			deps.Desktop.OpenBrowser = func(string) error { opened++; return nil }
			deps.Desktop.IsTerminal = func(io.Writer) bool { return true }

			require.NoError(t, runCLI(t, deps, "ui"))

			assert.Zero(t, opened)
			// The link is still printed. An operator on a machine reached over
			// SSH needs it, and a command that opened nothing and said nothing
			// would leave them with no way to reach the console at all.
			launchTicket(t, output.String())
			assert.Contains(t, output.String(), "Did not open a browser")
		})
	}
}

func TestAuthURLCopiesOnRequestAndNotOtherwise(t *testing.T) {
	deps, output, _ := uiDependencies(t)
	t.Setenv("NO_BROWSER", "1")
	var copied []string
	deps.Desktop.CopyToClipboard = func(_ context.Context, text string) error {
		copied = append(copied, text)
		return nil
	}

	require.NoError(t, runCLI(t, deps, "auth", "url"))
	assert.Empty(t, copied, "nothing reaches the clipboard without --copy")

	output.Reset()
	require.NoError(t, runCLI(t, deps, "auth", "url", "--copy"))
	printed := strings.TrimSpace(strings.SplitN(output.String(), "\n", 2)[0])
	assert.Equal(t, []string{printed}, copied)
	assert.Contains(t, output.String(), "Copied to the clipboard.")
}

// TestDevelopmentOpensTheConsoleOnceItIsListening is the first-run path: the
// operator types one command and gets a console, with no key to paste.
func TestDevelopmentOpensTheConsoleOnceItIsListening(t *testing.T) {
	deps, output, _ := uiDependencies(t)
	t.Setenv("CI", "")
	t.Setenv("NO_BROWSER", "")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	consoleURL := "http://" + listener.Addr().String() + localauth.LaunchPath + "?lt=ticket"
	deps.StartDevelopment = func(context.Context, GatewayOptions) (DevelopmentSession, error) {
		return DevelopmentSession{
			URL: "http://" + listener.Addr().String(), APIKey: "development-key",
			ConsoleURL: consoleURL,
			Run:        func(context.Context) error { return nil },
			Close:      func(context.Context) error { return nil },
		}, nil
	}
	opened := make(chan string, 1)
	deps.Desktop.OpenBrowser = func(target string) error { opened <- target; return nil }
	deps.Desktop.IsTerminal = func(io.Writer) bool { return true }

	require.NoError(t, runCLI(t, deps, "dev"))

	select {
	case target := <-opened:
		assert.Equal(t, consoleURL, target)
	case <-time.After(5 * time.Second):
		t.Fatal("the console was never opened")
	}
	assert.Contains(t, output.String(), consoleURL, "the link is printed as well as opened")
}

// TestDevelopmentOpensNoBrowserInAutomation is the acceptance criterion
// verbatim: with CI set, `starport dev` prints the URL and opens nothing.
func TestDevelopmentOpensNoBrowserInAutomation(t *testing.T) {
	deps, output, _ := uiDependencies(t)
	t.Setenv("CI", "1")
	consoleURL := "http://127.0.0.1:8080" + localauth.LaunchPath + "?lt=ticket"
	deps.StartDevelopment = func(context.Context, GatewayOptions) (DevelopmentSession, error) {
		return DevelopmentSession{
			URL: "http://127.0.0.1:8080", APIKey: "development-key",
			ConsoleURL: consoleURL,
			Run:        func(context.Context) error { return nil },
			Close:      func(context.Context) error { return nil },
		}, nil
	}
	deps.Desktop.OpenBrowser = func(string) error {
		t.Error("a browser was opened in automation")
		return nil
	}
	deps.Desktop.IsTerminal = func(io.Writer) bool { return true }

	require.NoError(t, runCLI(t, deps, "dev"))

	assert.Contains(t, output.String(), "http://127.0.0.1:8080")
	assert.Contains(t, output.String(), consoleURL)
}

// TestDevelopmentHonoursNoOpen covers the operator who wants the link and not
// the browser — a remote machine, or a browser profile they did not want used.
func TestDevelopmentHonoursNoOpen(t *testing.T) {
	deps, output, _ := uiDependencies(t)
	t.Setenv("CI", "")
	t.Setenv("NO_BROWSER", "")
	consoleURL := "http://127.0.0.1:8080" + localauth.LaunchPath + "?lt=ticket"
	deps.StartDevelopment = func(context.Context, GatewayOptions) (DevelopmentSession, error) {
		return DevelopmentSession{
			URL: "http://127.0.0.1:8080", APIKey: "development-key",
			ConsoleURL: consoleURL,
			Run:        func(context.Context) error { return nil },
			Close:      func(context.Context) error { return nil },
		}, nil
	}
	deps.Desktop.OpenBrowser = func(string) error {
		t.Error("--no-open opened a browser")
		return nil
	}
	deps.Desktop.IsTerminal = func(io.Writer) bool { return true }

	require.NoError(t, runCLI(t, deps, "dev", "--no-open"))
	assert.Contains(t, output.String(), consoleURL)
}

// TestTheWelcomePrintsOnce keeps the greeting from becoming noise. It is only
// true on a first run, and an operator who sees it on every start learns to
// scroll past the one screen that was written for them.
func TestTheWelcomePrintsOnce(t *testing.T) {
	deps, output, paths := uiDependencies(t)
	t.Setenv("CI", "1")

	require.NoError(t, runCLI(t, deps, "serve"))
	assert.Contains(t, output.String(), "starport ui")
	assert.FileExists(t, paths.WelcomeStampFile)

	output.Reset()
	require.NoError(t, runCLI(t, deps, "serve"))
	assert.NotContains(t, output.String(), "Welcome to Starport")
}

// TestTheWelcomeSeparatesTheTwoCredentials is the vocabulary the whole campaign
// exists to fix. The first thing a new operator reads must not leave them
// thinking the console wants a gateway API key.
func TestTheWelcomeSeparatesTheTwoCredentials(t *testing.T) {
	deps, output, _ := uiDependencies(t)
	t.Setenv("CI", "1")

	require.NoError(t, runCLI(t, deps, "serve"))

	greeting := output.String()
	assert.Contains(t, greeting, "starport ui")
	assert.Contains(t, greeting, "gateway API key")
	assert.Contains(t, greeting, "not for the")
}

// TestNoCommandPrintsTheLocalTokenWhileHandingOutALink guards the credential
// the links are derived from. A launch URL is safe to paste into a chat window
// for ninety seconds; the token it was signed with is not safe to paste ever.
func TestNoCommandPrintsTheLocalTokenWhileHandingOutALink(t *testing.T) {
	deps, output, paths := uiDependencies(t)
	t.Setenv("NO_BROWSER", "1")

	require.NoError(t, runCLI(t, deps, "ui", "--no-open"))
	require.NoError(t, runCLI(t, deps, "auth", "url"))

	store, err := localauth.NewStore(paths.LocalTokenFile)
	require.NoError(t, err)
	token, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.NotContains(t, output.String(), token.Secret)
}

// TestARedirectedRunIsNotATerminal states the third suppression rule directly.
// It is the one that catches `starport ui > link.txt` and every wrapper script,
// neither of which sets CI or NO_BROWSER. It runs against the real terminal
// check, which is what every other test in this file replaces.
func TestARedirectedRunIsNotATerminal(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("NO_BROWSER", "")
	assert.Equal(t, "output is not a terminal", browserSuppressed(Dependencies{}, &bytes.Buffer{}))
}
