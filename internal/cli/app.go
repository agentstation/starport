// Package cli owns Starport command contracts and process-independent execution.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/identity"
)

const (
	// ExitCodeRuntime reports an application or dependency failure.
	ExitCodeRuntime = 1
	// ExitCodeUsage reports invalid command syntax or arguments.
	ExitCodeUsage                 = 2
	initializationRollbackTimeout = 5 * time.Second
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
	// ErrConfigLoaderRequired reports a missing configuration reader.
	ErrConfigLoaderRequired = errors.New("configuration loader is required")
	// ErrPathResolverRequired reports a missing platform-path reader.
	ErrPathResolverRequired = errors.New("configuration path resolver is required")
	// ErrDiagnoserRequired reports a missing diagnostic runtime boundary.
	ErrDiagnoserRequired = errors.New("diagnoser is required")
)

const (
	flagNoAuth            = "no-auth"
	flagAllowRemoteNoAuth = "allow-remote-no-auth"
)

// jsonOutputUsage is what every --json flag says it does. The commands own
// separate flag constants because each names its own flag, but they describe
// one behaviour and a reader comparing two help screens should see one
// sentence.
const jsonOutputUsage = "Write machine-readable JSON"

// GatewayOptions carries the gateway decisions a command line can make. They
// are decisions, not configuration: everything else the gateway reads comes
// from the environment and the configuration file, and these exist because an
// operator has to be able to make them for one run without editing either.
type GatewayOptions struct {
	// DisableAuth serves requests without a gateway API key.
	DisableAuth bool
	// AllowRemoteNoAuth acknowledges an unauthenticated gateway on an address
	// the network can reach. It lifts a startup refusal and nothing else.
	AllowRemoteNoAuth bool
}

// ServerRunner starts the gateway and blocks until it stops.
type ServerRunner func(context.Context, GatewayOptions) error

// Initializer creates local state and returns the new gateway credential once.
type Initializer func(context.Context, InitOptions) (InitResult, error)

// ConfigLoader reads and validates effective configuration.
type ConfigLoader func(context.Context) (*config.Config, error)

// PathResolver resolves platform configuration and data paths.
type PathResolver func() (config.Paths, error)

// Diagnoser runs read-only startup checks.
type Diagnoser func(context.Context, diagnosis.Options) diagnosis.Report

type usageErrorHandler = urfavecli.OnUsageErrorFunc

// Dependencies contains all runtime boundaries used by commands.
type Dependencies struct {
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	Build            BuildInfo
	RunServer        ServerRunner
	StartDevelopment DevelopmentStarter
	Initialize       Initializer
	LoadConfig       ConfigLoader
	ResolvePaths     PathResolver
	Diagnose         Diagnoser
	// Desktop reaches the operator's machine. It is not validated: a machine
	// with no browser and no clipboard still runs every command, because each
	// one prints the link it would otherwise have handed over.
	Desktop Desktop
}

// New creates the Starport root command.
func New(deps Dependencies) (*urfavecli.Command, error) {
	if err := validateDependencies(deps); err != nil {
		return nil, err
	}

	var usageError usageErrorHandler = func(
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
				Name:  flagNoAuth,
				Usage: "Serve requests without a gateway API key",
			},
			&urfavecli.BoolFlag{
				Name:  flagAllowRemoteNoAuth,
				Usage: "Allow --no-auth on an address the network can reach",
			},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			options := GatewayOptions{
				DisableAuth:       cmd.Bool(flagNoAuth),
				AllowRemoteNoAuth: cmd.Bool(flagAllowRemoteNoAuth),
			}
			greetOnce(cmd.Writer, deps)
			if err := deps.RunServer(ctx, options); err != nil {
				return runtimeFailure{cause: err}
			}
			return nil
		},
	}
	development := &urfavecli.Command{
		Name:         "dev",
		Usage:        "Run an isolated local development gateway",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{
				Name:  flagNoAuth,
				Usage: "Serve requests without a gateway API key",
			},
			&urfavecli.BoolFlag{
				Name:  flagNoOpen,
				Usage: "Print the console link instead of opening a browser",
			},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			// The development gateway binds loopback and nothing else, so the
			// remote acknowledgment has no meaning here and no flag offers it.
			session, err := deps.StartDevelopment(ctx, GatewayOptions{DisableAuth: cmd.Bool(flagNoAuth)})
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if err := session.validate(); err != nil {
				if session.Close != nil {
					err = closeDevelopmentSession(ctx, session, err)
				}
				return runtimeFailure{cause: err}
			}
			if err := writeDevelopmentResult(cmd.Writer, session); err != nil {
				return runtimeFailure{cause: closeDevelopmentSession(ctx, session, err)}
			}
			greetOnce(cmd.Writer, deps)
			if session.ConsoleURL != "" && !cmd.Bool(flagNoOpen) {
				if reason := browserSuppressed(deps, cmd.Writer); reason != "" {
					if _, err := fmt.Fprintf(cmd.Writer, "Did not open a browser: %s.\n", reason); err != nil {
						return runtimeFailure{cause: closeDevelopmentSession(ctx, session, err)}
					}
				} else {
					// The browser is opened beside the gateway rather than before
					// it, because the listener is not up until Run is called and a
					// browser that arrives first shows a connection error.
					go openConsoleWhenReady(ctx, deps, session)
				}
			}
			if err := session.Run(ctx); err != nil {
				return runtimeFailure{cause: closeDevelopmentSession(ctx, session, err)}
			}
			if err := closeDevelopmentSession(ctx, session, nil); err != nil {
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
			configuredStorage := cmd.Bool("configured-storage")
			identityName := cmd.String("name")
			if err := identity.ValidateName(identityName); err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			result, initializeErr := deps.Initialize(ctx, InitOptions{
				IdentityName: identityName, ConfiguredStorage: configuredStorage,
			})
			if result.APIKey != "" {
				if err := writeInitResult(cmd.Writer, result, cmd.Bool(initFormatJSON)); err != nil {
					return runtimeFailure{cause: rollbackInitialization(ctx, result, err)}
				}
			}
			if initializeErr != nil {
				return runtimeFailure{cause: initializeErr}
			}
			return nil
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
	completion := &urfavecli.Command{
		Name:  "completion",
		Usage: "Generate a shell completion script",
	}
	man := &urfavecli.Command{
		Name:         "man",
		Usage:        "Generate the starport(1) manual page",
		OnUsageError: usageError,
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			if err := writeManPage(cmd.Root(), cmd.Writer); err != nil {
				return runtimeFailure{cause: err}
			}
			return nil
		},
	}
	configCommand := newConfigCommand(deps, usageError)
	doctor := newDoctorCommand(deps, usageError)
	auth := newAuthCommand(deps, usageError)
	ui := newUICommand(deps, usageError)

	root := &urfavecli.Command{
		Name:            "starport",
		Usage:           "OpenAI- and OpenRouter-compatible LLM inference gateway",
		Version:         deps.Build.Version,
		Reader:          deps.Stdin,
		Writer:          deps.Stdout,
		ErrWriter:       deps.Stderr,
		OnUsageError:    usageError,
		Commands:        []*urfavecli.Command{initialize, development, serve, ui, auth, doctor, configCommand, version, man, help},
		HideHelpCommand: true,
		ConfigureShellCompletionCommand: configureCompletionCommand(
			completion,
			usageError,
		),
		Suggest: true,
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

func validateDependencies(deps Dependencies) error {
	if deps.Stdin == nil {
		return ErrStdinRequired
	}
	if deps.Stdout == nil {
		return ErrStdoutRequired
	}
	if deps.Stderr == nil {
		return ErrStderrRequired
	}
	if deps.RunServer == nil {
		return ErrServerRunnerRequired
	}
	if deps.StartDevelopment == nil {
		return ErrDevelopmentStarterRequired
	}
	if deps.Initialize == nil {
		return ErrInitializerRequired
	}
	if deps.LoadConfig == nil {
		return ErrConfigLoaderRequired
	}
	if deps.ResolvePaths == nil {
		return ErrPathResolverRequired
	}
	if deps.Diagnose == nil {
		return ErrDiagnoserRequired
	}
	return nil
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

func rollbackInitialization(ctx context.Context, result InitResult, outputErr error) error {
	resultErr := fmt.Errorf("write initialization result: %w", outputErr)
	if result.Rollback == nil {
		return resultErr
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), initializationRollbackTimeout)
	defer cancel()
	if err := result.Rollback(rollbackCtx); err != nil {
		return errors.Join(resultErr, fmt.Errorf("rollback initialization: %w", err))
	}
	return resultErr
}
