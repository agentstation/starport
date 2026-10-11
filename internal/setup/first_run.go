package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/config"
)

const (
	// DefaultAPIKeyName names the first gateway API key of a local root.
	DefaultAPIKeyName = "local-admin"
	// RecoverCommand retries or removes a pending local setup transaction.
	RecoverCommand = "starport auth bootstrap --recover"
)

// ErrNoPendingSetup reports a local root without an interrupted setup
// transaction, so recovery has nothing to retry.
var ErrNoPendingSetup = errors.New("no interrupted local setup to recover")

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

// Recover retries an interrupted local setup transaction. A retry that
// completes the setup returns the new one-time key. A transaction that had
// already published its key settles to a ready root and returns an empty
// result, because no command can show that key again.
//
// Recover refuses a root with no pending transaction: an empty root belongs
// to the first run of serve, and a ready root has nothing to retry. It writes
// nothing to an empty root.
func (s *Service) Recover(ctx context.Context, request Request) (Result, error) {
	if err := s.validate(request); err != nil {
		return Result{}, err
	}
	state, err := Inspect(s.paths)
	switch {
	case err == nil && state == StateAbsent:
		return Result{}, fmt.Errorf("%w: the local root is empty; run %q to initialize it", ErrNoPendingSetup, "starport serve")
	case state == "":
		return Result{}, err
	}
	// The runtime guard refuses a root while a setup transaction is pending.
	// A root that it accepts has nothing for recovery to retry.
	guard, err := GuardLocalStorage(ctx, s.paths)
	if err == nil {
		if err := guard.Close(); err != nil {
			return Result{}, err
		}
		if state == StateReady {
			return Result{}, fmt.Errorf("%w: the local root is already initialized", ErrNoPendingSetup)
		}
		return Result{}, fmt.Errorf("%w: inspect %q and %q; no setup transaction wrote their contents",
			ErrNoPendingSetup, s.paths.ConfigDir, s.paths.DataDir)
	}
	if !errors.Is(err, ErrPartialState) {
		return Result{}, err
	}
	result, err := s.Initialize(ctx, request)
	if errors.Is(err, ErrAlreadyInitialized) {
		return Result{}, nil
	}
	return result, err
}
