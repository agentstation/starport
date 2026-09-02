package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/rs/zerolog/log"
	urfavecli "github.com/urfave/cli/v3"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/app"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/setup"
	"github.com/agentstation/starport/internal/storage"
)

// Build-time variables injected through linker flags.
var (
	version   = "dev"
	buildTime = "unknown"
	gitCommit = "unknown"
	gitBranch = "unknown"
	goVersion = "unknown"
)

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runContext(
		ctx,
		args,
		stdin,
		stdout,
		stderr,
		runServer,
		startDevelopment,
		runInitializer,
	)
}

func runContext(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	server starportcli.ServerRunner,
	development starportcli.DevelopmentStarter,
	initializer starportcli.Initializer,
) int {
	err := starportcli.Run(ctx, args, starportcli.Dependencies{
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Build: buildInformation(), RunServer: server,
		StartDevelopment: development, Initialize: initializer,
		ExtraCommands: processCommands(),
		LoadConfig: func(loadCtx context.Context) (*config.Config, error) {
			return config.LoadWithDefaults(loadCtx)
		},
		ResolvePaths: config.PlatformPaths,
		Diagnose:     diagnosis.Run,
	})
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(stderr, err)
	return starportcli.ExitCode(err)
}

// processCommands names the commands this process boundary owns: the agent
// surface binds to the build, because the embedded skill and the embedded
// catalog generation ship inside this binary.
func processCommands() []*urfavecli.Command {
	return []*urfavecli.Command{
		newModelsCommand(loadRoutableModels),
		newAgentCommand(),
	}
}

func runInitializer(ctx context.Context, options starportcli.InitOptions) (starportcli.InitResult, error) {
	if options.ConfiguredStorage {
		return initializeConfiguredStorage(ctx, options)
	}
	paths, err := config.PlatformPaths()
	if err != nil {
		return starportcli.InitResult{}, fmt.Errorf("resolve setup paths: %w", err)
	}
	service := setup.New(paths)
	result, err := service.Initialize(ctx, setup.Request{
		APIKeyName: options.APIKeyName,
	})
	initialized := starportcli.InitResult{
		APIKeyName: result.APIKeyName,
		ConfigFile: result.ConfigFile, DataDir: result.DataDir, APIKey: result.APIKey,
		Rollback: func(rollbackCtx context.Context) error {
			return service.Rollback(rollbackCtx, result)
		},
	}
	if result.APIKey == "" {
		initialized = starportcli.InitResult{}
	}
	return initialized, err
}

func startDevelopment(
	ctx context.Context,
	options starportcli.GatewayOptions,
) (starportcli.DevelopmentSession, error) {
	cfg, err := config.LoadDevelopment(ctx, configOverrides(options)...)
	if err != nil {
		return starportcli.DevelopmentSession{}, fmt.Errorf("load development configuration: %w", err)
	}
	runtime, err := app.NewDevelopment(ctx, cfg, app.WithBuildInfo(version, gitCommit, buildTime))
	if err != nil {
		return starportcli.DevelopmentSession{}, err
	}
	// A console link that could not be minted is not a reason to refuse a
	// gateway. The session still runs and still prints its URL, and the
	// operator reaches the console the same way they would on any other
	// deployment.
	consoleURL, err := runtime.ConsoleURL()
	if err != nil {
		log.Warn().Err(err).Msg("Could not mint a console launch link for this development gateway")
	}
	return starportcli.DevelopmentSession{
		URL: runtime.URL(), APIKey: runtime.APIKey(), ConsoleURL: consoleURL,
		AuthDisabled: cfg.Security.AuthMode.Effective() == config.AuthModeDisabled,
		Run:          runtime.Run, Close: runtime.Close,
	}, nil
}

// configOverrides translates command line decisions into configuration
// overrides. The translation happens once, here, so both commands reach the
// loader through the same path and meet the same validation.
func configOverrides(options starportcli.GatewayOptions) []config.Override {
	var overrides []config.Override
	if options.DisableAuth {
		overrides = append(overrides, config.DisableAuthentication())
	}
	if options.AllowRemoteNoAuth {
		overrides = append(overrides, config.AllowRemoteWithoutAuthentication())
	}
	return overrides
}

func initializeConfiguredStorage(
	ctx context.Context,
	options starportcli.InitOptions,
) (starportcli.InitResult, error) {
	cfg, err := config.LoadWithDefaults(ctx)
	if err != nil {
		return starportcli.InitResult{}, fmt.Errorf("load configured storage: %w", err)
	}
	storageConfig := cfg.Storage.RuntimeStorage()
	if storageConfig.Type == storage.StorageTypeBadger {
		storageConfig.Badger.SyncWrites = true
	}
	store, err := storage.Open(storageConfig)
	if err != nil {
		return starportcli.InitResult{}, fmt.Errorf("open configured storage: %w", err)
	}
	issued, issueErr := setup.InitializeAPIKey(ctx, store, options.APIKeyName)
	closeErr := store.Close()
	result := configuredInitResult(storageConfig, options.APIKeyName, issued)
	if issueErr != nil {
		return result, errors.Join(issueErr, closeErr)
	}
	if closeErr != nil {
		return result, fmt.Errorf("close configured storage: %w", closeErr)
	}
	return result, nil
}

func configuredInitResult(
	storageConfig storage.Config,
	apiKeyName string,
	issued apikey.IssueResult,
) starportcli.InitResult {
	if issued.Secret == "" {
		return starportcli.InitResult{}
	}
	return starportcli.InitResult{
		APIKeyName: apiKeyName,
		APIKey:     issued.Secret,
		Rollback: func(rollbackCtx context.Context) error {
			return rollbackConfiguredAPIKey(rollbackCtx, storageConfig, issued.APIKey.ID)
		},
	}
}

func rollbackConfiguredAPIKey(
	ctx context.Context,
	storageConfig storage.Config,
	apiKeyID string,
) error {
	store, err := storage.Open(storageConfig)
	if err != nil {
		return fmt.Errorf("open configured storage for rollback: %w", err)
	}
	releaseErr := setup.ReleaseAPIKey(ctx, store, apiKeyID)
	closeErr := store.Close()
	if releaseErr != nil {
		return errors.Join(releaseErr, closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close configured storage after rollback: %w", closeErr)
	}
	return nil
}

func runServer(ctx context.Context, options starportcli.GatewayOptions) error {
	cfg, err := config.LoadWithDefaults(ctx, configOverrides(options)...)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	application, err := app.New(cfg, app.WithBuildInfo(version, gitCommit, buildTime))
	if err != nil {
		return fmt.Errorf("create application: %w", err)
	}
	return application.Run(ctx)
}

func buildInformation() starportcli.BuildInfo {
	builtWith := goVersion
	if builtWith == "unknown" {
		builtWith = runtime.Version()
	}
	return starportcli.BuildInfo{
		Version: version, BuildTime: buildTime,
		GitCommit: gitCommit, GitBranch: gitBranch,
		GoVersion: builtWith, OS: runtime.GOOS, Arch: runtime.GOARCH,
	}
}
