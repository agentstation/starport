package main

import (
	"context"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/app"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/setup"
)

// gatewayRuntime is the opened application that serve blocks on.
type gatewayRuntime interface {
	Run(context.Context) error
}

// gatewayOpener builds the application from the final configuration.
type gatewayOpener func(*config.Config) (gatewayRuntime, error)

func runServer(ctx context.Context, options starportcli.GatewayOptions, output starportcli.ServerOutput) error {
	return serveGateway(ctx, options, output, openGateway)
}

func openGateway(cfg *config.Config) (gatewayRuntime, error) {
	application, err := app.New(cfg, app.WithBuildInfo(version, gitCommit, buildTime))
	if err != nil {
		return nil, err
	}
	return application, nil
}

// serveGateway composes serve. A clean local root is initialized first, so
// the one-time key prints before any other file lands in the data directory.
// The greeting waits until the application opens, so a failed run leaves no
// welcome stamp behind.
func serveGateway(
	ctx context.Context,
	options starportcli.GatewayOptions,
	output starportcli.ServerOutput,
	open gatewayOpener,
) error {
	overrides := configOverrides(options)
	cfg, err := config.LoadWithDefaults(ctx, overrides...)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	firstRun, err := initializeFirstRun(ctx, cfg, output)
	if err != nil {
		return err
	}
	if firstRun {
		// The loader reads the new master key from the configuration file.
		// The process environment stays unchanged.
		cfg, err = config.LoadWithDefaults(ctx, overrides...)
		if err != nil {
			return fmt.Errorf("reload configuration after initialization: %w", err)
		}
		if err := provisionMachineToken(ctx, cfg); err != nil {
			return err
		}
	}
	application, err := open(cfg)
	if err != nil {
		return fmt.Errorf("create application: %w", err)
	}
	output.Greet()
	return application.Run(ctx)
}

// initializeFirstRun initializes a clean local root and delivers its one-time
// key. It reports whether the root qualified for first-run initialization.
// A qualified root that another process initialized first reports true with
// no key, so the caller still reloads the configuration that process wrote.
func initializeFirstRun(ctx context.Context, cfg *config.Config, output starportcli.ServerOutput) (bool, error) {
	paths, err := config.PlatformPaths()
	if err != nil {
		return false, fmt.Errorf("resolve setup paths: %w", err)
	}
	if !setup.FirstRun(cfg, paths) {
		return false, nil
	}
	service := setup.New(paths)
	result, initializeErr := service.InitializeFirstRun(ctx, setup.Request{APIKeyName: starportcli.DefaultAPIKeyName})
	if err := output.DeliverCredential(ctx, localInitResult(service, result)); err != nil {
		return false, err
	}
	if initializeErr != nil {
		return false, fmt.Errorf("initialize local storage: %w", initializeErr)
	}
	return true, nil
}

// provisionMachineToken writes the local admin token before the gateway opens,
// so `starport auth token`, `starport ui`, and `starport auth url` read the
// token that the running gateway accepts.
func provisionMachineToken(ctx context.Context, cfg *config.Config) error {
	store, err := localauth.NewStore(cfg.Security.LocalTokenPath)
	if err != nil {
		return fmt.Errorf("open the local admin token: %w", err)
	}
	if _, _, err := store.LoadOrMint(ctx, time.Now()); err != nil {
		return fmt.Errorf("write the local admin token: %w", err)
	}
	return nil
}
