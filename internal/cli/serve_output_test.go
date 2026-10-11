package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServeDeliversTheFirstRunKeyBeforeTheWelcome keeps the order that a
// first run prints: the one-time key, then the greeting. The key output names
// no next step, because the gateway is already starting.
func TestServeDeliversTheFirstRunKeyBeforeTheWelcome(t *testing.T) {
	deps, output, paths := uiDependencies(t)
	deps.RunServer = func(ctx context.Context, _ GatewayOptions, server ServerOutput) error {
		if err := server.DeliverCredential(ctx, InitResult{
			APIKeyName: DefaultAPIKeyName,
			ConfigFile: paths.ConfigFile, DataDir: paths.DataDir, APIKey: "gateway-key",
		}); err != nil {
			return err
		}
		server.Greet()
		return nil
	}

	require.NoError(t, runCLI(t, deps, "serve"))

	printed := output.String()
	assert.Equal(t, 1, strings.Count(printed, "Gateway API key (shown once): gateway-key"))
	assert.Less(t, strings.Index(printed, "gateway-key"), strings.Index(printed, "Welcome to Starport."))
	assert.NotContains(t, printed, "Run: starport serve")
	assert.FileExists(t, paths.WelcomeStampFile)
}

// TestServeOutputRollsBackWhenTheKeyWriteFails keeps the init contract for
// serve: a key that nobody saw is rolled back, and the error reaches the
// operator as a runtime failure.
func TestServeOutputRollsBackWhenTheKeyWriteFails(t *testing.T) {
	deps, _, paths := uiDependencies(t)
	outputErr := errors.New("output unavailable")
	deps.Stdout = failingWriter{err: outputErr}
	rollbackCalls := 0
	deps.RunServer = func(ctx context.Context, _ GatewayOptions, server ServerOutput) error {
		return server.DeliverCredential(ctx, InitResult{
			APIKeyName: DefaultAPIKeyName, APIKey: "gateway-key",
			Rollback: func(context.Context) error {
				rollbackCalls++
				return nil
			},
		})
	}

	err := runCLI(t, deps, "serve")

	require.ErrorIs(t, err, outputErr)
	assert.Equal(t, ExitCodeRuntime, ExitCode(err))
	assert.Equal(t, 1, rollbackCalls)
	assert.NoFileExists(t, paths.WelcomeStampFile)
}

// TestServeDoesNotGreetAFailedStart keeps a failed first run from leaving the
// welcome stamp, which a later first-run inspection would count as state.
func TestServeDoesNotGreetAFailedStart(t *testing.T) {
	deps, output, paths := uiDependencies(t)
	startErr := errors.New("master key is required")
	deps.RunServer = func(context.Context, GatewayOptions, ServerOutput) error { return startErr }

	err := runCLI(t, deps, "serve")

	require.ErrorIs(t, err, startErr)
	assert.NotContains(t, output.String(), "Welcome to Starport")
	assert.NoFileExists(t, paths.WelcomeStampFile)
}
