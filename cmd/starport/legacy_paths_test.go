package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/setup"
	"github.com/stretchr/testify/require"
)

func legacyPathTestHome(t *testing.T) string {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "STARPORT_") || strings.HasPrefix(name, "STARMAP_") {
			t.Setenv(name, "")
			require.NoError(t, os.Unsetenv(name))
		}
	}
	home := t.TempDir()
	for name, path := range map[string]string{
		"HOME": home, "USERPROFILE": home,
		"XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"),
		"XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_CACHE_HOME": filepath.Join(home, "cache"),
		"APPDATA": filepath.Join(home, "roaming"), "LOCALAPPDATA": filepath.Join(home, "local"),
	} {
		t.Setenv(name, path)
	}
	return home
}

func TestConfiguredInitializationRefusesImplicitLegacyMove(t *testing.T) {
	oldConfig := filepath.Join(legacyPathTestHome(t), "previous-config")
	t.Setenv("STARPORT_CONFIG_DIR", oldConfig)
	previous := filepath.Join(oldConfig, "data", "badger")
	require.NoError(t, os.MkdirAll(previous, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(previous, "MANIFEST"), []byte("old gateway records"), 0o600))
	paths, err := config.PlatformPaths()
	require.NoError(t, err)
	result, err := initializeConfiguredStorage(t.Context(), starportcli.InitOptions{APIKeyName: "local-admin"})
	require.ErrorIs(t, err, config.ErrLegacyPaths)
	require.Empty(t, result.APIKey)
	require.NoDirExists(t, paths.DataDir)
	require.NoFileExists(t, paths.ConfigFile)
	contents, err := os.ReadFile(filepath.Join(previous, "MANIFEST"))
	require.NoError(t, err)
	require.Equal(t, "old gateway records", string(contents))
}

func TestPersistentStartupReportsLegacyPathsBeforeSetup(t *testing.T) {
	legacyPathTestHome(t)
	t.Setenv("STARPORT_SERVER_PORT", strconv.Itoa(freeLoopbackPort(t)))
	t.Setenv("STARPORT_CATALOG_NETWORK_MODE", "offline")
	t.Setenv("STARPORT_CATALOG_ACQUISITION_ENABLED", "false")
	paths, err := config.PlatformPaths()
	require.NoError(t, err)
	legacyRuntime, err := config.ResolveStateDirectory("")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(paths.BadgerDir, 0o700))
	require.NoError(t, os.MkdirAll(legacyRuntime, 0o700))
	marker := filepath.Join(legacyRuntime, "instance-seed")
	const oldSeed = "existing catalog seed"
	require.NoError(t, os.WriteFile(marker, []byte(oldSeed), 0o600))

	previousConfigRoot, err := os.UserConfigDir()
	require.NoError(t, err)
	previousConfig := filepath.Join(previousConfigRoot, "starport", "config.env")
	selector := "STARPORT_CATALOG_STATE_DIR"
	if previousConfig != paths.ConfigFile {
		require.NoError(t, os.MkdirAll(filepath.Dir(previousConfig), 0o700))
		require.NoError(t, os.WriteFile(previousConfig, []byte("STARPORT_SECURITY_MASTER_KEY="+strings.Repeat("m", 32)+"\n"), 0o600))
		selector = "STARPORT_CONFIG_FILE"
	}

	for _, command := range []string{"dev", "serve"} {
		t.Run(command, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			code := runContext(t.Context(), []string{"starport", command}, bytes.NewReader(nil), stdout, stderr,
				runServer, startDevelopment, runInitializer)
			diagnostic := stderr.String()
			require.Equal(t, starportcli.ExitCodeRuntime, code, diagnostic)
			require.Contains(t, diagnostic, config.ErrLegacyPaths.Error())
			require.Contains(t, diagnostic, selector)
			require.Contains(t, diagnostic, "starport config paths --legacy")
			require.NotContains(t, diagnostic, setup.RecoverCommand)
			require.Empty(t, stdout.String())
			require.NoFileExists(t, paths.ConfigFile)
			require.NoDirExists(t, paths.RuntimeDir)
			seed, err := os.ReadFile(marker)
			require.NoError(t, err)
			require.Equal(t, oldSeed, string(seed))
		})
	}
}

func TestMacUpgradeMigratesConfigurationAndCatalogRuntime(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the native configuration file moved on macOS")
	}
	legacyPathTestHome(t)
	port := freeLoopbackPort(t)
	t.Setenv("STARPORT_SERVER_PORT", strconv.Itoa(port))
	t.Setenv("STARPORT_CATALOG_NETWORK_MODE", "offline")
	t.Setenv("STARPORT_CATALOG_ACQUISITION_ENABLED", "false")
	current, err := config.PlatformPaths()
	require.NoError(t, err)
	fresh, err := config.LoadWithDefaults(t.Context())
	require.NoError(t, err)
	require.Equal(t, current.ConfigFile, fresh.EffectivePaths().ConfigFile)
	require.Equal(t, current.RuntimeDir, fresh.EffectivePaths().RuntimeDir)
	oldConfigRoot, err := os.UserConfigDir()
	require.NoError(t, err)
	oldConfig := filepath.Join(oldConfigRoot, "starport", "config.env")
	oldRuntime, err := config.ResolveStateDirectory("")
	require.NoError(t, err)
	t.Setenv("STARPORT_CONFIG_FILE", oldConfig)
	t.Setenv("STARPORT_CATALOG_STATE_DIR", oldRuntime)
	oldPaths, err := config.PlatformPaths()
	require.NoError(t, err)
	existing, err := setup.New(oldPaths).InitializeFirstRun(t.Context(), setup.Request{APIKeyName: setup.DefaultAPIKeyName})
	require.NoError(t, err)
	require.NotEmpty(t, existing.APIKey)

	var fileID string
	code, stderr := commandUntilReady(t, []string{"starport", "dev", "--no-open"}, port, &firstRunOutput{},
		func(ctx context.Context, base string) { fileID = uploadDevFile(ctx, t, base, existing.APIKey) })
	require.Zero(t, code, stderr)
	require.NotEmpty(t, fileID)
	oldMasterKey := masterKeyDigest(t, oldConfig)
	marker := filepath.Join(oldRuntime, "instance-seed")
	seed, err := os.ReadFile(marker)
	require.NoError(t, err)

	require.NoError(t, os.Unsetenv("STARPORT_CONFIG_FILE"))
	require.NoError(t, os.Unsetenv("STARPORT_CATALOG_STATE_DIR"))
	var conflicts, diagnostic bytes.Buffer
	code = runContext(t.Context(), []string{"starport", "config", "paths", "--legacy", "--json"},
		bytes.NewReader(nil), &conflicts, &diagnostic, runServer, startDevelopment, runInitializer)
	require.Zero(t, code, diagnostic.String())
	require.JSONEq(t, "[]", conflicts.String())

	require.NoError(t, os.MkdirAll(current.RuntimeDir, 0o700))
	ambiguous, err := config.LoadWithDefaults(t.Context())
	require.NoError(t, err)
	require.Equal(t, current.RuntimeDir, ambiguous.EffectivePaths().RuntimeDir)
	require.ErrorContains(t, ambiguous.CheckLegacyPaths(t.Context()), "STARPORT_CATALOG_STATE_DIR")
	require.NoError(t, os.Remove(current.RuntimeDir))

	require.NoError(t, os.MkdirAll(current.ConfigDir, 0o700))
	require.NoError(t, os.WriteFile(current.ConfigFile, []byte("# current configuration\n"), 0o600))
	ambiguous, err = config.LoadWithDefaults(t.Context())
	require.NoError(t, err)
	require.Equal(t, current.ConfigFile, ambiguous.EffectivePaths().ConfigFile)
	require.ErrorContains(t, ambiguous.CheckLegacyPaths(t.Context()), "STARPORT_CONFIG_FILE")
	require.NoError(t, os.Remove(current.ConfigFile))
	require.NoError(t, os.Remove(current.ConfigDir))

	owner := filepath.Join(oldRuntime, "owner.json")
	backupOwner := filepath.Join(filepath.Dir(oldRuntime), "test-owner.json")
	require.NoError(t, os.Rename(owner, backupOwner))
	var upgradeFailure bytes.Buffer
	code = runContext(t.Context(), []string{"starport", "dev", "--no-open"},
		bytes.NewReader(nil), &bytes.Buffer{}, &upgradeFailure, runServer, startDevelopment, runInitializer)
	require.Equal(t, starportcli.ExitCodeRuntime, code)
	require.Contains(t, upgradeFailure.String(), "catalog runtime ownership is absent")
	require.NoFileExists(t, current.ConfigFile)
	require.NoDirExists(t, current.RuntimeDir)
	require.FileExists(t, oldConfig)
	require.NoError(t, os.Rename(backupOwner, owner))

	for _, args := range [][]string{{"starport", "dev", "--no-open"}, {"starport", "serve"}} {
		output := &firstRunOutput{}
		var status int
		code, stderr = commandUntilReady(t, args, port, output, func(ctx context.Context, base string) {
			status = fileStatusWithKey(ctx, t, base, existing.APIKey, fileID)
		})
		require.Zero(t, code, stderr)
		require.Equal(t, http.StatusOK, status)
		require.Empty(t, printedGatewayKey(output.String()))
	}
	require.Equal(t, oldMasterKey, masterKeyDigest(t, current.ConfigFile))
	retained, err := os.ReadFile(filepath.Join(current.RuntimeDir, filepath.Base(marker)))
	require.NoError(t, err)
	require.Equal(t, seed, retained)
	require.NoFileExists(t, oldConfig)
	selected, err := config.LoadWithDefaults(t.Context())
	require.NoError(t, err)
	require.Equal(t, current.ConfigFile, selected.EffectivePaths().ConfigFile)
	require.Equal(t, current.RuntimeDir, selected.EffectivePaths().RuntimeDir)
	require.NoError(t, selected.CheckLegacyPaths(t.Context()))
}
