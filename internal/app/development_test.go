package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/storage"
)

func TestDevUsesInMemoryBadger(t *testing.T) {
	cfg := validProductionConfig(t)
	persistentPath := filepath.Join(t.TempDir(), "persistent-badger")
	cfg.Storage.Badger.Path = persistentPath
	cfg.Providers = config.ProvidersConfig{}

	runtime, err := NewDevelopment(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	projected := cfg.Storage.RuntimeStorage()
	require.Equal(t, storage.StorageTypeBadger, projected.Type)
	require.True(t, projected.Badger.InMemory)
	require.Empty(t, projected.Badger.Path)
	_, isBadger := runtime.application.store.(*storage.BadgerStore)
	require.True(t, isBadger)
	_, err = os.Stat(persistentPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestDevKeepsTheLocalTokenOffDisk is the release gate's claim in miniature: a
// development gateway creates nothing under the configuration directory. The
// token is minted in memory, the launch link still comes from it in-process,
// and the file the loader named is never touched.
func TestDevKeepsTheLocalTokenOffDisk(t *testing.T) {
	cfg := validProductionConfig(t)
	tokenPath := cfg.Security.LocalTokenPath
	require.NotEmpty(t, tokenPath, "the loader-shaped config names a token file")
	configuredFilesPath := cfg.Files.Path
	cfg.Providers = config.ProvidersConfig{}

	runtime, err := NewDevelopment(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	require.NoFileExists(t, tokenPath)
	require.NoFileExists(t, tokenPath+".lock")
	require.NotEqual(t, configuredFilesPath, cfg.Files.Path,
		"stored file bytes land in a session-owned scratch directory")
	consoleURL, err := runtime.ConsoleURL()
	require.NoError(t, err)
	require.NotEmpty(t, consoleURL, "the in-memory token still mints a launch link")
}

// TestDevAcceptsTheMachineTokenWithoutTouchingIt covers the machine that has
// run a serving gateway before: the development gateway reads the token the
// CLI prints, so the console paste path and `starport ui` agree with it, and
// the file itself is left byte-for-byte alone.
func TestDevAcceptsTheMachineTokenWithoutTouchingIt(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Providers = config.ProvidersConfig{}
	store, err := localauth.NewStore(cfg.Security.LocalTokenPath)
	require.NoError(t, err)
	machine, _, err := store.LoadOrMint(context.Background(), time.Now())
	require.NoError(t, err)
	before, err := os.ReadFile(cfg.Security.LocalTokenPath)
	require.NoError(t, err)

	runtime, err := NewDevelopment(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	ticket, err := localauth.MintTicket(machine, time.Now())
	require.NoError(t, err)
	_, _, err = runtime.application.localGate.Redeem(ticket, time.Now())
	require.NoError(t, err, "a ticket minted from the machine token opens the development console")

	after, err := os.ReadFile(cfg.Security.LocalTokenPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "the development gateway did not rewrite the machine token")
}

// TestDevRemovesItsScratchFileStorageOnClose is the other half of the file
// story: the scratch directory is working memory, and a session that kept it
// would leave state behind after all.
func TestDevRemovesItsScratchFileStorageOnClose(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Providers = config.ProvidersConfig{}

	runtime, err := NewDevelopment(t.Context(), cfg)
	require.NoError(t, err)
	scratch := cfg.Files.Path
	require.DirExists(t, scratch)

	require.NoError(t, runtime.Close(context.Background()))
	require.NoDirExists(t, scratch)
}

func TestDevBindsLoopbackOnly(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 18991
	cfg.Providers = config.ProvidersConfig{}

	runtime, err := NewDevelopment(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })

	require.Equal(t, "127.0.0.1", cfg.Server.Host)
	require.Equal(t, "http://127.0.0.1:18991", runtime.URL())
}

func TestDevRejectsCanceledContextBeforeCreatingState(t *testing.T) {
	cfg := validProductionConfig(t)
	persistentPath := filepath.Join(t.TempDir(), "persistent-badger")
	cfg.Storage.Badger.Path = persistentPath
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	runtime, err := NewDevelopment(cancelled, cfg)
	require.Nil(t, runtime)
	require.ErrorIs(t, err, context.Canceled)
	_, statErr := os.Stat(persistentPath)
	require.True(t, errors.Is(statErr, os.ErrNotExist))
}
