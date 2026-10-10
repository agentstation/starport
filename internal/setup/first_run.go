package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/config"
)

// RecoverCommand retries or removes a pending local setup transaction.
const RecoverCommand = "starport auth bootstrap --recover"

// FirstRun reports whether a gateway may initialize its own local root.
//
// Only a loopback gateway with local management, no master key, and every
// store at its platform default qualifies. A shared or external authority, a
// configured store, or a supplied master key keeps the explicit path, so a
// cluster node never mints its own master key. A gateway that binds a network
// address keeps the explicit path too, so the one-time key never prints into a
// container log.
func FirstRun(cfg *config.Config, paths config.Paths) bool {
	return cfg != nil && strings.TrimSpace(cfg.Security.MasterKey) == "" &&
		cfg.ManagementMode() == config.ManagementLocal &&
		authmode.LoopbackHost(cfg.Server.Host) &&
		cfg.EffectivePaths().ConfigFile == paths.ConfigFile &&
		cfg.UsesPlatformStorage(paths)
}

// InitializeFirstRun initializes an absent local root and does nothing to a
// ready one. A partial root returns ErrPartialState with recovery guidance;
// it never retries a pending transaction. A concurrent winner leaves a ready
// root, so the loser returns an empty result and reads the winner's files.
func (s *Service) InitializeFirstRun(ctx context.Context, request Request) (Result, error) {
	if err := s.validate(request); err != nil {
		return Result{}, err
	}
	state, err := Inspect(s.paths)
	switch {
	case state == StatePartial:
		return Result{}, errors.Join(partialState(s.paths.ConfigFile), err)
	case err != nil:
		return Result{}, err
	case state == StateReady:
		return Result{}, nil
	}
	result, err := s.Initialize(ctx, request)
	if errors.Is(err, ErrAlreadyInitialized) {
		return Result{}, nil
	}
	return result, err
}

// partialState names the recovery step for an incomplete local root.
func partialState(configFile string) error {
	if configFile == "" {
		return fmt.Errorf("%w: run %q", ErrPartialState, RecoverCommand)
	}
	return fmt.Errorf("%w: inspect %q, then run %q", ErrPartialState, configFile, RecoverCommand)
}
