package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/urfave/cli/v2"

	"github.com/agentstation/starport/internal/app"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
)

// Build-time variables injected via ldflags
var (
	version   = "dev"
	buildTime = "unknown"
	gitCommit = "unknown"
	gitBranch = "unknown"
	goVersion = "unknown"
)

// run is the entry point for the application. It sets up the context and signal handling.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runApp(ctx)
}

// runApp is the main function for the application. It sets up the CLI app and it's commands
// including version, serve, and parse.
func runApp(ctx context.Context) error {
	cliApp := &cli.App{
		Name:    "starport",
		Usage:   "High-performance LLM gateway",
		Version: version,
		Commands: []*cli.Command{
			{
				Name:  "version",
				Usage: "Show detailed version information",
				Action: func(_ *cli.Context) error {
					fmt.Printf("Starport %s\n", version)
					fmt.Printf("Build Time:  %s\n", buildTime)
					fmt.Printf("Git Commit:  %s\n", gitCommit)
					fmt.Printf("Git Branch:  %s\n", gitBranch)
					fmt.Printf("Go Version:  %s\n", getBuildGoVersion())
					fmt.Printf("OS/Arch:     %s/%s\n", runtime.GOOS, runtime.GOARCH)

					if info, ok := debug.ReadBuildInfo(); ok {
						fmt.Printf("\nBuild Info:\n")
						fmt.Printf("  Main Module: %s\n", info.Main.Path)
						if info.Main.Sum != "" {
							fmt.Printf("  Module Sum:  %s\n", info.Main.Sum)
						}
						fmt.Printf("  Go Version:  %s\n", info.GoVersion)

						if len(info.Settings) > 0 {
							fmt.Printf("\nBuild Settings:\n")
							for _, setting := range info.Settings {
								if setting.Key == "vcs.revision" || setting.Key == "vcs.time" || setting.Key == "vcs.modified" {
									fmt.Printf("  %s: %s\n", setting.Key, setting.Value)
								}
							}
						}
					}
					return nil
				},
			},
			{
				Name:    "serve",
				Aliases: []string{"server"},
				Usage:   "Run the llm gateway server",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "enable-ollama",
						Usage:   "Enable Ollama support for local models",
						EnvVars: []string{"STARPORT_ENABLE_OLLAMA"},
					},
				},
				Action: func(c *cli.Context) error {
					return runServer(ctx, c.Bool("enable-ollama"))
				},
			},
		},
		Action: func(c *cli.Context) error {
			// If no subcommand is provided, show help
			return cli.ShowAppHelp(c)
		},
	}

	return cliApp.RunContext(ctx, os.Args)
}

// runServer is the main function for the application. It sets up the server and it's configuration.
func runServer(ctx context.Context, enableOllama bool) error {
	// Load configuration
	cfg, err := config.LoadWithDefaults(ctx)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	
	// Override Ollama setting from command line flag
	if enableOllama {
		cfg.Providers.Ollama.Enabled = true
	}

	// Build app options from loaded config
	opts := []app.Option{
		app.WithServerConfig(server.Config{
			Port: cfg.Server.Port,
			Host: cfg.Server.Host,
		}),
		app.WithStorageMode(cfg.Storage.Mode),
		app.WithStorageConfig(&cfg.Storage),
		app.WithLogLevel(cfg.Logging.Level),
		app.WithProvidersConfig(&cfg.Providers),
		app.WithCache(cfg.Cache.Enabled),
	}

	// Add hot reload config if enabled
	if cfg.RateLimiting.EnableHotReload {
		opts = append(opts, app.WithHotReload(&app.HotReloadConfig{
			Enabled:       true,
			ConfigPath:    cfg.RateLimiting.ConfigPath,
			CheckInterval: cfg.RateLimiting.ReloadCheckInterval,
		}))
	}

	// Add ChatUI config if enabled
	if cfg.ChatUI.Enabled {
		opts = append(opts, app.WithChatUI(&cfg.ChatUI))
	}

	app, err := app.New(opts...)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}

	return app.Run(ctx)
}

// getBuildGoVersion is a helper function to get the build-time Go version.
func getBuildGoVersion() string {
	// Use the build-time injected version if available
	if goVersion != "unknown" {
		return goVersion
	}
	// Fall back to runtime version
	return runtime.Version()
}
