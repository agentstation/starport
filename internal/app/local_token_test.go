package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/localauth"
)

// TestStartupMintsTheTokenTheCLIReads is the claim the whole credential rests
// on: the value `starport auth token` prints is the value the running gateway
// is holding. Two files that happened to agree in a comment would not be worth
// much; this reads what startup wrote, through the store the CLI opens.
func TestStartupMintsTheTokenTheCLIReads(t *testing.T) {
	cfg := validProductionConfig(t)
	path := cfg.Security.LocalTokenPath
	require.NoFileExists(t, path, "the machine starts cold")

	application, err := New(cfg, withTestFactories())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })

	store, err := localauth.NewStore(path)
	require.NoError(t, err)
	token, err := store.Load(context.Background())
	require.NoError(t, err)
	require.NoError(t, token.Validate())
	assert.Equal(t, uint64(1), token.Generation)
	assert.False(t, token.Rotated(), "a token startup minted has not been rotated")
}

// TestASecondStartKeepsTheFirstToken covers the restart. Minting again would
// silently invalidate the token an operator has already copied somewhere, and
// they would find out when their console session stopped working.
func TestASecondStartKeepsTheFirstToken(t *testing.T) {
	cfg := validProductionConfig(t)
	first, err := New(cfg, withTestFactories())
	require.NoError(t, err)
	require.NoError(t, first.Close(context.Background()))

	store, err := localauth.NewStore(cfg.Security.LocalTokenPath)
	require.NoError(t, err)
	afterFirst, err := store.Load(context.Background())
	require.NoError(t, err)

	second, err := New(cfg, withTestFactories())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close(context.Background())) })

	afterSecond, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, afterFirst, afterSecond)
}

// TestANetworkBindRefusesAFirstBootToken is the AON8 acceptance case. The
// refusal has to name the command that clears it, because an operator who is
// told only that the token is unrotated has no way to act on it.
func TestANetworkBindRefusesAFirstBootToken(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Server.Host = "0.0.0.0"

	_, err := New(cfg, withTestFactories())

	require.ErrorIs(t, err, ErrLocalTokenExposed)
	assert.Contains(t, err.Error(), localauth.RotateCommand)
	assert.Contains(t, err.Error(), "0.0.0.0", "the message names the address that caused the refusal")
}

// TestANetworkBindAcceptsARotatedToken is the other half: the refusal has to be
// one an operator can actually get past by doing what it says.
func TestANetworkBindAcceptsARotatedToken(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Server.Host = "0.0.0.0"
	store, err := localauth.NewStore(cfg.Security.LocalTokenPath)
	require.NoError(t, err)
	_, err = store.Rotate(context.Background(), time.Now())
	require.NoError(t, err)

	application, err := New(cfg, withTestFactories())

	require.NoError(t, err)
	require.NoError(t, application.Close(context.Background()))
}

// TestLoopbackAcceptsAFirstBootToken keeps the refusal from reaching the case it
// exists to permit. A laptop has to start with no ceremony at all.
func TestLoopbackAcceptsAFirstBootToken(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Server.Host = "127.0.0.1"

	application, err := New(cfg, withTestFactories())

	require.NoError(t, err)
	require.NoError(t, application.Close(context.Background()))
}

// TestCompositionRefusesAConfigurationWithNowhereToKeepTheToken fails closed.
// The loader always fills this path in, so an empty value means the
// configuration did not come from the loader, and picking a path here would put
// a credential somewhere the CLI never looks for it.
func TestCompositionRefusesAConfigurationWithNowhereToKeepTheToken(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Security.LocalTokenPath = ""

	_, err := New(cfg, withTestFactories())

	require.ErrorIs(t, err, ErrLocalTokenPathRequired)
}

// TestStartupRefusesAnUnreadableTokenRatherThanReplacingIt protects a
// credential an operator may still be holding. Overwriting a file that failed
// to decode would be the destructive answer to a recoverable problem, and
// `starport auth rotate` is the recovery.
func TestStartupRefusesAnUnreadableTokenRatherThanReplacingIt(t *testing.T) {
	cfg := validProductionConfig(t)
	path := cfg.Security.LocalTokenPath
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))

	_, err := New(cfg, withTestFactories())

	require.ErrorIs(t, err, localauth.ErrCorruptRecord)
	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("{"), after)
}

func withTestFactories() func(*buildOptions) {
	factories := explicitTestFactories()
	return func(options *buildOptions) { options.factories = factories }
}
