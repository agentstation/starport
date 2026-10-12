package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/app"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/endpoint"
)

// developerPorts returns the ports that a development gateway tries, in
// order, when no setting names its port. first is the configured port.
type developerPorts func(first int) []int

func startDevelopment(
	ctx context.Context,
	options starportcli.DevelopmentOptions,
	deliver starportcli.CredentialDelivery,
) (starportcli.DevelopmentSession, error) {
	return startDevelopmentOn(ctx, options, deliver, func(first int) []int {
		return endpoint.DeveloperPorts(first, rand.IntN)
	})
}

// startDevelopmentOn starts a development session that binds the first free
// port in ports.
func startDevelopmentOn(
	ctx context.Context,
	options starportcli.DevelopmentOptions,
	deliver starportcli.CredentialDelivery,
	ports developerPorts,
) (starportcli.DevelopmentSession, error) {
	if options.Ephemeral {
		return startEphemeralDevelopment(ctx, options.GatewayOptions, ports)
	}
	return startPersistentDevelopment(ctx, options.GatewayOptions, deliver, ports)
}

// bindDevelopment binds the development listener before the gateway opens.
// When a setting names the port, it binds only that port. Otherwise it tries
// ports in order and sets the configured port to the bound port, so every URL
// that the gateway derives names the bound port. It returns the first port in
// the order as busy when the bound port differs.
func bindDevelopment(ctx context.Context, cfg *config.Config, ports developerPorts) (net.Listener, int, error) {
	order := []int{cfg.Server.Port}
	if !cfg.ServerPortExplicit() {
		order = ports(cfg.Server.Port)
	}
	listener, err := endpoint.BindFirst(ctx, cfg.Server.Host, order, endpoint.Listen)
	if err != nil {
		return nil, 0, app.BindFailure(err)
	}
	busy := 0
	if bound := endpoint.Port(listener.Addr()); bound != order[0] {
		busy = order[0]
	}
	cfg.Server.Port = endpoint.Port(listener.Addr())
	return listener, busy, nil
}

// startPersistentDevelopment runs the serve composition over the persistent
// development profile. A clean root prints its key through deliver. The
// listener binds first, so a busy port fails before any key exists and the
// root stays clean.
func startPersistentDevelopment(
	ctx context.Context,
	options starportcli.GatewayOptions,
	deliver starportcli.CredentialDelivery,
	ports developerPorts,
) (starportcli.DevelopmentSession, error) {
	overrides := configOverrides(options)
	load := func(ctx context.Context) (*config.Config, error) {
		return config.LoadPersistentDevelopment(ctx, overrides...)
	}
	cfg, err := load(ctx)
	if err != nil {
		return starportcli.DevelopmentSession{}, fmt.Errorf("load configuration: %w", err)
	}
	listener, busy, err := bindDevelopment(ctx, cfg, ports)
	if err != nil {
		return starportcli.DevelopmentSession{}, err
	}
	cfg, err = initializeGateway(ctx, cfg, load, deliver)
	if err != nil {
		_ = listener.Close()
		return starportcli.DevelopmentSession{}, err
	}
	// A first run reloads the configuration, which names the configured port
	// again and not the bound port.
	cfg.Server.Port = endpoint.Port(listener.Addr())
	runtime, err := app.NewPersistentDevelopment(cfg, app.WithBuildInfo(version, gitCommit, buildTime), app.WithListener(listener))
	if err != nil {
		// A rollback can close the listener first. A second close changes nothing.
		_ = listener.Close()
		return starportcli.DevelopmentSession{}, fmt.Errorf("create application: %w", err)
	}
	return developmentSession(cfg, runtime, busy), nil
}

// startEphemeralDevelopment keeps every store in memory or in a scratch
// directory that the session removes on exit.
func startEphemeralDevelopment(
	ctx context.Context,
	options starportcli.GatewayOptions,
	ports developerPorts,
) (starportcli.DevelopmentSession, error) {
	cfg, err := config.LoadDevelopment(ctx, configOverrides(options)...)
	if err != nil {
		return starportcli.DevelopmentSession{}, fmt.Errorf("load development configuration: %w", err)
	}
	listener, busy, err := bindDevelopment(ctx, cfg, ports)
	if err != nil {
		return starportcli.DevelopmentSession{}, err
	}
	runtime, err := app.NewDevelopment(ctx, cfg, app.WithBuildInfo(version, gitCommit, buildTime), app.WithListener(listener))
	if err != nil {
		// A rollback can close the listener first. A second close changes nothing.
		_ = listener.Close()
		return starportcli.DevelopmentSession{}, err
	}
	return developmentSession(cfg, runtime, busy), nil
}

func developmentSession(cfg *config.Config, runtime *app.Development, busyPort int) starportcli.DevelopmentSession {
	// A console-link failure does not stop the gateway.
	// The session still runs and prints its URL. The operator uses the normal console access procedure.
	consoleURL, err := runtime.ConsoleURL()
	if err != nil {
		log.Warn().Err(err).Msg("Could not mint a console launch link for this development gateway")
	}
	return starportcli.DevelopmentSession{
		URL: runtime.URL(), APIKey: runtime.APIKey(), ConsoleURL: consoleURL,
		AuthDisabled: cfg.Security.AuthMode.Effective() == config.AuthModeDisabled,
		BusyPort:     busyPort,
		Run:          runtime.Run, Close: runtime.Close,
	}
}
