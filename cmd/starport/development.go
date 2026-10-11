package main

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/app"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
)

func startDevelopment(
	ctx context.Context,
	options starportcli.DevelopmentOptions,
	deliver starportcli.CredentialDelivery,
) (starportcli.DevelopmentSession, error) {
	if options.Ephemeral {
		return startEphemeralDevelopment(ctx, options.GatewayOptions)
	}
	return startPersistentDevelopment(ctx, options.GatewayOptions, deliver)
}

// startPersistentDevelopment runs the serve composition over the persistent
// development profile. A clean root prints its key through deliver.
func startPersistentDevelopment(
	ctx context.Context,
	options starportcli.GatewayOptions,
	deliver starportcli.CredentialDelivery,
) (starportcli.DevelopmentSession, error) {
	overrides := configOverrides(options)
	cfg, err := prepareGateway(ctx, func(ctx context.Context) (*config.Config, error) {
		return config.LoadPersistentDevelopment(ctx, overrides...)
	}, deliver)
	if err != nil {
		return starportcli.DevelopmentSession{}, err
	}
	runtime, err := app.NewPersistentDevelopment(cfg, app.WithBuildInfo(version, gitCommit, buildTime))
	if err != nil {
		return starportcli.DevelopmentSession{}, fmt.Errorf("create application: %w", err)
	}
	return developmentSession(cfg, runtime), nil
}

// startEphemeralDevelopment keeps every store in memory or in a scratch
// directory that the session removes on exit.
func startEphemeralDevelopment(
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
	return developmentSession(cfg, runtime), nil
}

func developmentSession(cfg *config.Config, runtime *app.Development) starportcli.DevelopmentSession {
	// A console-link failure does not stop the gateway.
	// The session still runs and prints its URL. The operator uses the normal console access procedure.
	consoleURL, err := runtime.ConsoleURL()
	if err != nil {
		log.Warn().Err(err).Msg("Could not mint a console launch link for this development gateway")
	}
	return starportcli.DevelopmentSession{
		URL: runtime.URL(), APIKey: runtime.APIKey(), ConsoleURL: consoleURL,
		AuthDisabled: cfg.Security.AuthMode.Effective() == config.AuthModeDisabled,
		Run:          runtime.Run, Close: runtime.Close,
	}
}
