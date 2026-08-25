package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/agentstation/starport/internal/localauth"
)

const (
	flagNoOpen = "no-open"
	flagOpen   = "open"
	flagCopy   = "copy"
)

// launchURL mints a launch ticket and returns the URL that spends it.
//
// It reads the configuration for the address and the token file for the
// secret, and it asks the running gateway for nothing. A command that had to
// reach the gateway to produce a sign-in URL would fail in the one case where
// an operator needs it most: a gateway that is up but not letting them in.
func launchURL(ctx context.Context, deps Dependencies) (string, error) {
	cfg, err := deps.LoadConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("read configuration: %w", err)
	}
	store, err := localauth.NewStore(cfg.Security.LocalTokenPath)
	if err != nil {
		return "", err
	}
	// Minting here matches `starport auth token`: an operator who runs this on a
	// cold machine gets a working URL rather than an instruction to go start
	// something first. The gateway will read the same file when it starts.
	token, _, err := store.LoadOrMint(ctx, time.Now())
	if err != nil {
		return "", err
	}
	ticket, err := localauth.MintTicket(token, time.Now())
	if err != nil {
		return "", err
	}
	base := localauth.BrowsableBase(cfg.Server.Host, cfg.Server.Port, cfg.Security.EnableTLS)
	return localauth.LaunchURL(base, ticket)
}

func newUICommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "ui",
		Usage: "Open the Starport console in a browser",
		Description: "Mints a one-time launch link from this machine's local admin token and " +
			"opens it. The console signs in with a session cookie, so no gateway API key is " +
			"pasted into a browser and none is stored there.",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{
				Name:  flagNoOpen,
				Usage: "Print the launch link instead of opening a browser",
			},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			url, err := launchURL(ctx, deps)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			return writeLaunch(ctx, cmd.Writer, deps, url, !cmd.Bool(flagNoOpen), false)
		},
	}
}

// newAuthURLCommand is the same link under the credential command, for an
// operator who is already in `starport auth` because the console stopped
// letting them in.
func newAuthURLCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name:         "url",
		Usage:        "Print a one-time launch link for the console",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{Name: flagOpen, Usage: "Open the link in a browser"},
			&urfavecli.BoolFlag{Name: flagCopy, Usage: "Copy the link to the clipboard"},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			url, err := launchURL(ctx, deps)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			return writeLaunch(ctx, cmd.Writer, deps, url, cmd.Bool(flagOpen), cmd.Bool(flagCopy))
		},
	}
}

// writeLaunch prints the link, and optionally opens or copies it.
//
// The link is printed either way. An operator whose browser did not come up,
// or who is on a machine reached over SSH, still needs the URL, and a command
// that opened something and said nothing leaves them with no way to retry.
func writeLaunch(
	ctx context.Context,
	writer io.Writer,
	deps Dependencies,
	url string,
	open bool,
	copyIt bool,
) error {
	if _, err := fmt.Fprintln(writer, url); err != nil {
		return runtimeFailure{cause: err}
	}
	if copyIt {
		if err := clipboardWriter(deps)(ctx, url); err != nil {
			return runtimeFailure{cause: err}
		}
		if _, err := fmt.Fprintln(writer, "Copied to the clipboard."); err != nil {
			return runtimeFailure{cause: err}
		}
	}
	if !open {
		return nil
	}
	if reason := browserSuppressed(deps, writer); reason != "" {
		_, err := fmt.Fprintf(writer, "Did not open a browser: %s.\n", reason)
		return err
	}
	if err := browserOpener(deps)(url); err != nil {
		// A browser that will not start is not a failed command. The operator is
		// holding the link, which is the thing they asked for.
		_, printErr := fmt.Fprintf(writer, "Could not open a browser: %v.\nOpen the link above.\n", err)
		return printErr
	}
	return nil
}

// browserSuppressed names why this environment gets a printed link instead of
// a browser, or returns empty when opening one is right.
//
// Each case is somewhere a browser either does not exist or would open on the
// wrong machine. A launch link works once and expires in TicketTTL, so an
// automated run that quietly opened one would burn the ticket and leave the
// operator with a dead link.
func browserSuppressed(deps Dependencies, writer io.Writer) string {
	if os.Getenv("CI") != "" {
		return "CI is set"
	}
	if os.Getenv("NO_BROWSER") != "" {
		return "NO_BROWSER is set"
	}
	if !terminalCheck(deps)(writer) {
		return "output is not a terminal"
	}
	return ""
}
