// Package cli owns Starport command contracts and process-independent execution.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/agentstation/starmap/pkg/catalogs"
	urfavecli "github.com/urfave/cli/v3"
)

const (
	// ExitCodeRuntime reports an application or dependency failure.
	ExitCodeRuntime = 1
	// ExitCodeUsage reports invalid command syntax or arguments.
	ExitCodeUsage = 2
)

var (
	// ErrStdinRequired reports a missing command input stream.
	ErrStdinRequired = errors.New("command input is required")
	// ErrStdoutRequired reports a missing command output stream.
	ErrStdoutRequired = errors.New("command output is required")
	// ErrStderrRequired reports a missing command error stream.
	ErrStderrRequired = errors.New("command error output is required")
	// ErrServerRunnerRequired reports a missing server runtime boundary.
	ErrServerRunnerRequired = errors.New("server runner is required")
	// ErrInitializerRequired reports a missing setup runtime boundary.
	ErrInitializerRequired = errors.New("initializer is required")
)

// ServeOptions contains explicit server command options.
type ServeOptions struct {
	EnableOllama bool
}

// ServerRunner starts the gateway and blocks until it stops.
type ServerRunner func(context.Context, ServeOptions) error

// Initializer creates local state and returns the new gateway credential once.
type Initializer func(context.Context, InitOptions) (InitResult, error)

// Dependencies contains all runtime boundaries used by commands.
type Dependencies struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Build      BuildInfo
	RunServer  ServerRunner
	Initialize Initializer
}

// New creates the Starport root command.
func New(deps Dependencies) (*urfavecli.Command, error) {
	if deps.Stdin == nil {
		return nil, ErrStdinRequired
	}
	if deps.Stdout == nil {
		return nil, ErrStdoutRequired
	}
	if deps.Stderr == nil {
		return nil, ErrStderrRequired
	}
	if deps.RunServer == nil {
		return nil, ErrServerRunnerRequired
	}
	if deps.Initialize == nil {
		return nil, ErrInitializerRequired
	}

	usageError := func(
		_ context.Context,
		cmd *urfavecli.Command,
		err error,
		isSubcommand bool,
	) error {
		if isSubcommand {
			_ = urfavecli.ShowSubcommandHelp(cmd)
		} else {
			_ = urfavecli.ShowAppHelp(cmd)
		}
		return urfavecli.Exit(err, ExitCodeUsage)
	}

	serve := &urfavecli.Command{
		Name:         "serve",
		Aliases:      []string{"server"},
		Usage:        "Run the LLM gateway server",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{
				Name:  "enable-ollama",
				Usage: "Enable Ollama for local model inference",
			},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			if err := deps.RunServer(ctx, ServeOptions{EnableOllama: cmd.Bool("enable-ollama")}); err != nil {
				return runtimeFailure{cause: err}
			}
			return nil
		},
	}

	initialize := &urfavecli.Command{
		Name:         "init",
		Usage:        "Initialize secure local configuration and identity storage",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:  "provider",
				Usage: "Local provider profile: openai or ollama",
			},
			&urfavecli.StringFlag{
				Name:  "name",
				Usage: "Name for the first gateway identity",
				Value: "local-admin",
			},
			&urfavecli.BoolFlag{
				Name:  initFormatJSON,
				Usage: "Write initialization results as JSON",
			},
			&urfavecli.BoolFlag{
				Name:  "configured-storage",
				Usage: "Create only the first identity in configured storage",
			},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			provider := catalogs.ProviderID(cmd.String("provider"))
			configuredStorage := cmd.Bool("configured-storage")
			if configuredStorage && provider != "" {
				return urfavecli.Exit(
					"--provider and --configured-storage cannot be used together",
					ExitCodeUsage,
				)
			}
			if !configuredStorage && provider != catalogs.ProviderIDOpenAI && provider != catalogs.ProviderIDOllama {
				return urfavecli.Exit(
					fmt.Sprintf("unsupported local provider profile %q; use --provider openai or --provider ollama", provider),
					ExitCodeUsage,
				)
			}
			result, err := deps.Initialize(ctx, InitOptions{
				Provider: provider, IdentityName: cmd.String("name"), ConfiguredStorage: configuredStorage,
			})
			if err != nil {
				return runtimeFailure{cause: err}
			}
			return writeInitResult(cmd.Writer, result, cmd.Bool(initFormatJSON))
		},
	}

	version := &urfavecli.Command{
		Name:         "version",
		Usage:        "Show build information",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{
				Name:  versionFormatJSON,
				Usage: "Write build information as JSON",
			},
		},
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			return WriteVersion(cmd.Writer, deps.Build, cmd.Bool(versionFormatJSON))
		},
	}

	help := &urfavecli.Command{
		Name:         "help",
		Usage:        "Show command help",
		ArgsUsage:    "[command]",
		OnUsageError: usageError,
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			switch cmd.NArg() {
			case 0:
				return urfavecli.ShowAppHelp(cmd.Root())
			case 1:
				return urfavecli.ShowCommandHelp(ctx, cmd.Root(), cmd.Args().First())
			default:
				return usageError(
					ctx,
					cmd,
					fmt.Errorf("help accepts at most one command"),
					true,
				)
			}
		},
	}

	root := &urfavecli.Command{
		Name:            "starport",
		Usage:           "OpenAI- and OpenRouter-compatible LLM inference gateway",
		Version:         deps.Build.Version,
		Reader:          deps.Stdin,
		Writer:          deps.Stdout,
		ErrWriter:       deps.Stderr,
		OnUsageError:    usageError,
		Commands:        []*urfavecli.Command{initialize, serve, version, help},
		HideHelpCommand: true,
		Suggest:         true,
		ExitErrHandler: func(context.Context, *urfavecli.Command, error) {
			// The process boundary owns error output and exit behavior.
		},
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			if cmd.NArg() > 0 {
				_ = urfavecli.ShowAppHelp(cmd)
				return urfavecli.Exit(
					fmt.Sprintf("unknown command %q", cmd.Args().First()),
					ExitCodeUsage,
				)
			}
			return urfavecli.ShowAppHelp(cmd)
		},
	}
	return root, nil
}

// Run builds and executes the command for the supplied argument slice.
func Run(ctx context.Context, args []string, deps Dependencies) error {
	command, err := New(deps)
	if err != nil {
		return err
	}
	return command.Run(ctx, args)
}

// ExitCode maps a command error to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var runtimeErr runtimeFailure
	if errors.As(err, &runtimeErr) {
		return ExitCodeRuntime
	}
	var coder urfavecli.ExitCoder
	if errors.As(err, &coder) {
		if coder.ExitCode() == 0 {
			return 0
		}
		return ExitCodeUsage
	}
	return ExitCodeRuntime
}

type runtimeFailure struct {
	cause error
}

func (failure runtimeFailure) Error() string {
	return failure.cause.Error()
}

func (failure runtimeFailure) Unwrap() error {
	return failure.cause
}

func rejectArguments(cmd *urfavecli.Command) error {
	if cmd.NArg() == 0 {
		return nil
	}
	return urfavecli.Exit(
		fmt.Sprintf("%s does not accept arguments", cmd.FullName()),
		ExitCodeUsage,
	)
}
