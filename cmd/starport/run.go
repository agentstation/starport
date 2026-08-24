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

	"github.com/agentstation/starport/internal/app"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/identity"
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
		IdentityName: options.IdentityName,
	})
	initialized := starportcli.InitResult{
		IdentityName: result.IdentityName,
		ConfigFile:   result.ConfigFile, DataDir: result.DataDir, APIKey: result.APIKey,
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
	runtime, err := app.NewDevelopment(ctx, cfg)
	if err != nil {
		return starportcli.DevelopmentSession{}, err
	}
	return starportcli.DevelopmentSession{
		URL: runtime.URL(), APIKey: runtime.APIKey(),
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
	issued, issueErr := setup.InitializeIdentity(ctx, store, options.IdentityName)
	closeErr := store.Close()
	result := configuredInitResult(storageConfig, options.IdentityName, issued)
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
	identityName string,
	issued identity.IssueResult,
) starportcli.InitResult {
	if issued.Secret == "" {
		return starportcli.InitResult{}
	}
	return starportcli.InitResult{
		IdentityName: identityName,
		APIKey:       issued.Secret,
		Rollback: func(rollbackCtx context.Context) error {
			return rollbackConfiguredIdentity(rollbackCtx, storageConfig, issued.APIKey.ID)
		},
	}
}

func rollbackConfiguredIdentity(
	ctx context.Context,
	storageConfig storage.Config,
	identityID string,
) error {
	store, err := storage.Open(storageConfig)
	if err != nil {
		return fmt.Errorf("open configured storage for rollback: %w", err)
	}
	releaseErr := setup.ReleaseIdentity(ctx, store, identityID)
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
	application, err := app.New(cfg)
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
