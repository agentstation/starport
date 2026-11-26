package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/setup"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func localPathUpgradeFixture(t *testing.T) (func(context.Context) (*config.Config, error), map[string]string, string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the native configuration file moved on macOS")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "previous-state"))
	configRoot, err := os.UserConfigDir()
	require.NoError(t, err)
	previousConfig := filepath.Join(configRoot, "starport", "config.env")
	environment := map[string]string{
		"STARPORT_CATALOG_NETWORK_MODE":        "offline",
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}
	load := func(ctx context.Context) (*config.Config, error) {
		return config.NewLoader().WithEnvironment(environment).Load(ctx)
	}
	legacy, err := load(t.Context())
	require.NoError(t, err)
	oldPaths := legacy.EffectivePaths()
	oldPaths.ConfigFile = previousConfig
	_, err = setup.New(oldPaths).InitializeFirstRun(t.Context(), setup.Request{APIKeyName: setup.DefaultAPIKeyName})
	require.NoError(t, err)
	return load, environment, previousConfig
}

func TestLocalPathUpgradeResumesAfterEachPhase(t *testing.T) {
	load, environment, previousConfig := localPathUpgradeFixture(t)
	previousRuntime, err := config.ResolveStateDirectory("")
	require.NoError(t, err)
	environment["STARPORT_CONFIG_FILE"] = previousConfig
	environment["STARPORT_CATALOG_STATE_DIR"] = previousRuntime
	legacy, err := load(t.Context())
	require.NoError(t, err)
	store, err := storage.Open(legacy.RuntimeStorage())
	require.NoError(t, err)
	connected, err := catalog.OpenRuntime(t.Context(), store, catalogSettings(legacy), nil)
	require.NoError(t, err)
	identity := connected.Status().InstanceIdentity
	require.NoError(t, connected.Close(t.Context()))
	require.NoError(t, store.Close())
	delete(environment, "STARPORT_CONFIG_FILE")
	delete(environment, "STARPORT_CATALOG_STATE_DIR")

	stopped := errors.New("interrupted upgrade")
	for _, phase := range []string{prepareUpgradeRuntime, publishUpgradeRuntime, publishUpgradeConfig, completeUpgradeRuntime, retireUpgradeConfig} {
		selected, err := load(t.Context())
		require.NoError(t, err)
		_, err = upgradeLocalPathsWithCheckpoint(t.Context(), selected, load, func(current string) error {
			if current == phase {
				return stopped
			}
			return nil
		})
		require.ErrorIs(t, err, stopped, phase)
	}

	selected, err := load(t.Context())
	require.NoError(t, err)
	selected, err = UpgradeLocalPaths(t.Context(), selected, load)
	require.NoError(t, err)
	paths := selected.EffectivePaths()
	require.NoFileExists(t, previousConfig)
	require.FileExists(t, paths.ConfigFile)
	require.FileExists(t, filepath.Join(paths.DataDir, pathUpgradeDirectory, pathUpgradeSource))
	require.NoFileExists(t, filepath.Join(paths.DataDir, pathUpgradeDirectory, pathUpgradeRecord))
	require.NoFileExists(t, filepath.Join(paths.DataDir, pathUpgradeDirectory, pathUpgradeTarget))
	require.Equal(t, legacy.EffectivePaths().BadgerDir, paths.BadgerDir)
	require.True(t, legacy.Security.MasterKey == selected.Security.MasterKey)
	require.NoError(t, selected.VerifySavedRuntimeSelection(t.Context(), paths.RuntimeDir, identity))
}

func TestLocalPathUpgradeConcurrentConfigurationOnly(t *testing.T) {
	load, _, previousConfig := localPathUpgradeFixture(t)
	original, err := os.ReadFile(previousConfig)
	require.NoError(t, err)
	original = append(original, []byte("\n# retain this comment\n")...)
	require.NoError(t, os.WriteFile(previousConfig, original, 0o600))
	const operationsName = ".starport-config-operations.json"
	operations := []byte(`{"operations":[{"operation_id":"prior-save","status":"complete"}]}`)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(previousConfig), operationsName), operations, 0o600))
	selected, err := load(t.Context())
	require.NoError(t, err)
	_, target, ok := selected.DefaultLocalPathUpgrade()
	require.True(t, ok)

	published, proceed := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, err := upgradeLocalPathsWithCheckpoint(t.Context(), selected, load, func(phase string) error {
			if phase == publishUpgradeConfig {
				close(published)
				<-proceed
			}
			return nil
		})
		first <- err
	}()
	select {
	case <-published:
	case err := <-first:
		t.Fatalf("first startup stopped before publication: %v", err)
	}
	concurrent, loadErr := load(t.Context())
	var contention error
	if loadErr == nil {
		_, contention = UpgradeLocalPaths(t.Context(), concurrent, load)
	}
	close(proceed)
	require.NoError(t, <-first)
	require.NoError(t, loadErr)
	require.ErrorIs(t, contention, setup.ErrPartialState)
	require.ErrorContains(t, contention, "another process owns local setup")
	concurrent, err = load(t.Context())
	require.NoError(t, err)
	_, err = UpgradeLocalPaths(t.Context(), concurrent, load)
	require.NoError(t, err)
	replacement, err := os.ReadFile(target.ConfigFile)
	require.NoError(t, err)
	require.True(t, bytes.Equal(original, replacement), "configuration content changed")
	retained, err := os.ReadFile(filepath.Join(target.ConfigDir, operationsName))
	require.NoError(t, err)
	require.Equal(t, operations, retained)
	require.NoFileExists(t, previousConfig)
	require.NoDirExists(t, target.RuntimeDir)
}
