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
	return runContext(ctx, args, stdin, stdout, stderr, runServer, runInitializer)
}

func runContext(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	server starportcli.ServerRunner,
	initializer starportcli.Initializer,
) int {
	err := starportcli.Run(ctx, args, starportcli.Dependencies{
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Build: buildInformation(), RunServer: server, Initialize: initializer,
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
	result, err := setup.New(paths).Initialize(ctx, setup.Request{
		Provider:           options.Provider,
		ProviderCredential: os.Getenv(setup.OpenAIProviderCredentialEnvironment),
		IdentityName:       options.IdentityName,
	})
	if err != nil {
		return starportcli.InitResult{}, err
	}
	return starportcli.InitResult{
		Provider: result.Provider, IdentityName: result.IdentityName,
		ConfigFile: result.ConfigFile, DataDir: result.DataDir, APIKey: result.APIKey,
	}, nil
}

func initializeConfiguredStorage(
	ctx context.Context,
	options starportcli.InitOptions,
) (starportcli.InitResult, error) {
	cfg, err := config.LoadWithDefaults(ctx)
	if err != nil {
		return starportcli.InitResult{}, fmt.Errorf("load configured storage: %w", err)
	}
	store, err := storage.Open(cfg.Storage.RuntimeStorage())
	if err != nil {
		return starportcli.InitResult{}, fmt.Errorf("open configured storage: %w", err)
	}
	issued, issueErr := setup.InitializeIdentity(ctx, store, options.IdentityName)
	closeErr := store.Close()
	if issueErr != nil {
		return starportcli.InitResult{}, errors.Join(issueErr, closeErr)
	}
	if closeErr != nil {
		return starportcli.InitResult{}, fmt.Errorf("close configured storage: %w", closeErr)
	}
	return starportcli.InitResult{
		IdentityName: options.IdentityName,
		APIKey:       issued.Secret,
	}, nil
}

func runServer(ctx context.Context, options starportcli.ServeOptions) error {
	cfg, err := config.LoadWithDefaults(ctx)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if options.EnableOllama {
		cfg.Providers.Ollama.Enabled = true
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
