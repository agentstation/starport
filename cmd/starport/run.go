package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/rs/zerolog/log"
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

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runApp(ctx)
}

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
				Usage:   "Run the gateway server",
				Action: func(_ *cli.Context) error {
					return runServer(ctx)
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

func runServer(ctx context.Context) error {
	// Load configuration
	cfg, err := config.LoadWithDefaults(ctx)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Convert to app config
	appConfig := &app.Config{
		Server: server.Config{
			Port: cfg.Server.Port,
			Host: cfg.Server.Host,
		},
		StorageMode: cfg.Storage.Mode,
		LogLevel:    cfg.Logging.Level,
	}

	// Initialize hot reloader if enabled
	var hotReloader *config.HotReloader
	if cfg.RateLimiting.EnableHotReload {
		hotReloader, err = config.NewHotReloader(
			cfg.RateLimiting.ConfigPath,
			cfg.RateLimiting.ReloadCheckInterval,
		)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to initialize rate limit hot reloader")
		} else {
			if err := hotReloader.Start(ctx); err != nil {
				log.Warn().Err(err).Msg("Failed to start rate limit hot reloader")
			} else {
				defer hotReloader.Stop()
				log.Info().
					Str("config_path", cfg.RateLimiting.ConfigPath).
					Msg("Rate limit hot reload enabled")
			}
		}
	}

	app, err := app.New(
		app.WithConfig(appConfig),
		app.WithHotReloader(hotReloader),
		app.WithProvidersConfig(&cfg.Providers),
	)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}

	return app.Run(ctx)
}

func getBuildGoVersion() string {
	// Use the build-time injected version if available
	if goVersion != "unknown" {
		return goVersion
	}
	// Fall back to runtime version
	return runtime.Version()
}
