package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs/permission/hostclock/profile"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func nativeClockProfile() profile.Config {
	return profile.Config{
		Source: profile.Native, RefreshInterval: 10 * time.Second,
		MaxAge: time.Minute, MaxDriftPPM: 500, CounterUncertainty: time.Microsecond,
		Windows: profile.Windows{MaxSourceAge: time.Hour, MaxSourceDriftPPM: 500, SourceUncertainty: time.Millisecond},
	}
}

func TestConfiguredPermissionClockLifecycle(t *testing.T) {
	for _, shutdown := range []string{"close", "canceled construction context"} {
		t.Run(shutdown, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:8080")
			settings.PermissionClock = nativeClockProfile()
			connected, err := OpenRuntime(ctx, storage.NewMockStore(), settings, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
			require.Eventually(t, func() bool {
				return connected.runtime.PermissionClockStatus().Attempts > 0
			}, 10*time.Second, time.Millisecond)
			// Unqualified native hosts still retain catalog diagnostics.
			require.True(t, connected.runtime.PermissionClockStatus().Running)
			require.True(t, connected.Status().CatalogAvailable)
			if shutdown == "canceled construction context" {
				cancel()
				// Runtime ownership outlives the construction context. Its owner calls Close.
				require.True(t, connected.runtime.PermissionClockStatus().Running)
			}
			require.NoError(t, connected.Close(context.Background()))
			status := connected.runtime.PermissionClockStatus()
			require.False(t, status.Running)
			require.False(t, status.Known)
		})
	}
}

func TestConfiguredPermissionClockDisabledStartsNoMonitor(t *testing.T) {
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:8080")
	settings.PermissionClock.Source = profile.Disabled
	connected, err := OpenRuntime(t.Context(), storage.NewMockStore(), settings, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
	status := connected.runtime.PermissionClockStatus()
	require.False(t, status.Running)
	require.False(t, status.Known)
	require.Zero(t, status.Attempts)
	require.True(t, connected.Status().CatalogAvailable)
}

func TestConfiguredPermissionClockRefusesBeforeStoreConstruction(t *testing.T) {
	settings := Settings{PermissionClock: profile.Config{Source: profile.Native}}
	_, err := OpenRuntime(t.Context(), nil, settings, nil)
	require.ErrorContains(t, err, "permission_clock")
}
