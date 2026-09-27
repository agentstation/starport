package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBadgerDurableWriteProfile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		sync   bool
	}{
		{name: "durable default", sync: true},
		{name: "explicit asynchronous", values: map[string]string{"STARPORT_STORAGE_BADGER_SYNC_WRITES": "false"}},
		{name: "explicit synchronous", values: map[string]string{"STARPORT_STORAGE_BADGER_SYNC_WRITES": "true"}, sync: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(tc.values).Load(t.Context())
			require.NoError(t, err)
			require.Equal(t, tc.sync, cfg.RuntimeStorage().Badger.SyncWrites)
			require.Equal(t, tc.sync, cfg.Storage.Badger.SyncWrites)
			development, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(tc.values).LoadDevelopment(t.Context())
			require.NoError(t, err)
			require.False(t, development.RuntimeStorage().Badger.SyncWrites)
			require.True(t, development.RuntimeStorage().Badger.InMemory)
		})
	}
}

func TestBadgerMaintenanceProjection(t *testing.T) {
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_STORAGE_BADGER_COMPRESSION":      "zstd",
		"STARPORT_STORAGE_BADGER_GC_INTERVAL":      "7m",
		"STARPORT_STORAGE_BADGER_GC_DISCARD_RATIO": "0.3",
	}).Load(t.Context())
	require.NoError(t, err)
	selected := cfg.RuntimeStorage().Badger
	require.Equal(t, "zstd", selected.Compression)
	require.Equal(t, 7*time.Minute, selected.GCInterval)
	require.Equal(t, 0.3, selected.GCDiscardRatio)
	for _, ratio := range []float64{0, 1, -0.1, 1.1} {
		cfg.Storage.Badger.GCDiscardRatio = ratio
		require.Error(t, cfg.Storage.Badger.Validate())
	}
}
