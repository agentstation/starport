package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/agentstation/starport/internal/app"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
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
	return runContext(ctx, args, stdin, stdout, stderr, runServer)
}

func runContext(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	server starportcli.ServerRunner,
) int {
	err := starportcli.Run(ctx, args, starportcli.Dependencies{
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Build: buildInformation(), RunServer: server,
	})
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(stderr, err)
	return starportcli.ExitCode(err)
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
