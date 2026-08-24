package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/agentstation/starport/internal/localauth"
)

const authFormatJSON = "json"

// authStatusView is what `starport auth status` reports. It never carries the
// secret: a status command is the one an operator runs in front of other
// people, and a credential in that output is a credential in a screen share.
type authStatusView struct {
	Present    bool       `json:"present"`
	TokenFile  string     `json:"token_file"`
	Generation uint64     `json:"generation,omitempty"`
	IssuedAt   *time.Time `json:"issued_at,omitempty"`
	RotatedAt  *time.Time `json:"rotated_at,omitempty"`
	// AllowsNetworkBind is the AON8 tripwire's answer for this token. It is
	// here because it is the reason an operator runs the command: they are
	// about to bind a public address and want to know whether it will start.
	AllowsNetworkBind bool `json:"allows_network_bind"`
}

func newAuthCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	jsonFlag := func() urfavecli.Flag {
		return &urfavecli.BoolFlag{
			Name:  authFormatJSON,
			Usage: jsonOutputUsage,
		}
	}
	openStore := func() (*localauth.Store, error) {
		paths, err := deps.ResolvePaths()
		if err != nil {
			return nil, errors.New("resolve configuration paths: platform paths could not be resolved")
		}
		return localauth.NewStore(paths.LocalTokenFile)
	}

	token := &urfavecli.Command{
		Name: "token", Usage: "Print this machine's local admin token",
		OnUsageError: usageError, Flags: []urfavecli.Flag{jsonFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return runtimeFailure{cause: err}
			}
			// Minting here rather than refusing means an operator who runs this
			// before ever starting the gateway gets a token instead of an
			// instruction to go start one. It is the same token the first start
			// would have minted.
			current, _, err := store.LoadOrMint(ctx, time.Now())
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if err := writeAuthToken(cmd.Writer, current, store.Path(), cmd.Bool(authFormatJSON)); err != nil {
				return runtimeFailure{cause: fmt.Errorf("write the local admin token: %w", err)}
			}
			return nil
		},
	}
	status := &urfavecli.Command{
		Name: "status", Usage: "Show the local admin token's age, generation, and exposure",
		OnUsageError: usageError, Flags: []urfavecli.Flag{jsonFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return runtimeFailure{cause: err}
			}
			view := authStatusView{TokenFile: store.Path()}
			current, err := store.Load(ctx)
			switch {
			case err == nil:
				view.Present = true
				view.Generation = current.Generation
				issuedAt := current.IssuedAt
				view.IssuedAt = &issuedAt
				view.RotatedAt = current.RotatedAt
				view.AllowsNetworkBind = current.Rotated()
			case errors.Is(err, localauth.ErrNotFound):
				// A machine that has never started has no token, and that is
				// the ordinary answer rather than a failure.
			default:
				return runtimeFailure{cause: err}
			}
			if err := writeAuthStatus(cmd.Writer, view, cmd.Bool(authFormatJSON)); err != nil {
				return runtimeFailure{cause: fmt.Errorf("write the local admin token status: %w", err)}
			}
			return nil
		},
	}
	rotate := &urfavecli.Command{
		Name: "rotate", Usage: "Replace the local admin token with a new secret",
		OnUsageError: usageError, Flags: []urfavecli.Flag{jsonFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return runtimeFailure{cause: err}
			}
			rotated, err := store.Rotate(ctx, time.Now())
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if err := writeAuthRotation(cmd.Writer, rotated, store.Path(), cmd.Bool(authFormatJSON)); err != nil {
				return runtimeFailure{cause: fmt.Errorf("write the rotated local admin token: %w", err)}
			}
			return nil
		},
	}
	return &urfavecli.Command{
		Name: "auth", Usage: "Manage this machine's local admin token",
		Description: "The local admin token is a file on this machine. It is not a gateway " +
			"API key: it belongs to nobody, it is never issued to a tenant, and holding it " +
			"is a claim about where you are rather than who you are.",
		OnUsageError: usageError, Commands: []*urfavecli.Command{token, status, rotate},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.NArg() == 0 {
				return urfavecli.ShowSubcommandHelp(cmd)
			}
			return usageError(ctx, cmd, fmt.Errorf("unknown auth command %q", cmd.Args().First()), true)
		},
	}
}

// writeAuthToken prints the secret and nothing else in text mode, so the
// command composes: `starport auth token | pbcopy` has to copy a token and not
// a paragraph about one.
func writeAuthToken(writer io.Writer, token localauth.Token, path string, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(writer, struct {
			localauth.Token
			TokenFile string `json:"token_file"`
		}{Token: token, TokenFile: path})
	}
	_, err := fmt.Fprintln(writer, token.Secret)
	return err
}

func writeAuthStatus(writer io.Writer, view authStatusView, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(writer, view)
	}
	if !view.Present {
		_, err := fmt.Fprintf(
			writer,
			"Local admin token: none yet\nToken file: %s\n\nStarport mints one the first time it starts. Run \"starport auth token\" to mint it now.\n",
			view.TokenFile,
		)
		return err
	}
	rotated := "never"
	if view.RotatedAt != nil {
		rotated = view.RotatedAt.UTC().Format(time.RFC3339)
	}
	issued := ""
	if view.IssuedAt != nil {
		issued = view.IssuedAt.UTC().Format(time.RFC3339)
	}
	bind := "allowed"
	if !view.AllowsNetworkBind {
		bind = fmt.Sprintf(
			"refused until you run %q, because this token has only ever been printed at startup",
			localauth.RotateCommand,
		)
	}
	_, err := fmt.Fprintf(
		writer,
		"Local admin token: present\nToken file: %s\nGeneration: %d\nIssued: %s\nRotated: %s\nNetwork bind: %s\n",
		view.TokenFile, view.Generation, issued, rotated, bind,
	)
	return err
}

// writeAuthRotation says what changed and what has not. A rotation writes a
// file; a gateway that is already running keeps the token it loaded at startup,
// and an operator who does not know that will conclude the new token is broken.
func writeAuthRotation(writer io.Writer, token localauth.Token, path string, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(writer, struct {
			localauth.Token
			TokenFile string `json:"token_file"`
		}{Token: token, TokenFile: path})
	}
	_, err := fmt.Fprintf(
		writer,
		"Rotated the local admin token to generation %d.\nToken file: %s\n\n%s\n\n%s\n",
		token.Generation,
		path,
		token.Secret,
		"A running gateway keeps the token it read at startup. Restart it for this one to take effect.",
	)
	return err
}
