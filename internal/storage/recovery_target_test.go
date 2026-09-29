package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryTargetBindsKVScopeWithoutCredentials(t *testing.T) {
	cfg := Config{Type: StorageTypeValkey, Valkey: ValkeyConfig{URL: "redis://operator:secret@localhost:6379/2", DeploymentID: "one"}}
	before, err := cfg.RecoveryTargetSHA256("process-one")
	require.NoError(t, err)
	cfg.Valkey.URL = "redis://different:replacement@localhost:6379/2"
	after, err := cfg.RecoveryTargetSHA256("process-one")
	require.NoError(t, err)
	require.Equal(t, before, after)
	for _, changed := range []ValkeyConfig{
		{URL: "redis://localhost:6379/3", DeploymentID: "one"},
		{URL: "redis://localhost:6380/2", DeploymentID: "one"},
		{URL: "redis://localhost:6379/2", DeploymentID: "two"},
	} {
		cfg.Valkey = changed
		after, err = cfg.RecoveryTargetSHA256("process-one")
		require.NoError(t, err)
		require.NotEqual(t, before, after)
	}
	cfg.Valkey = ValkeyConfig{URL: "redis://localhost:6379/2", DeploymentID: "one"}
	after, err = cfg.RecoveryTargetSHA256("process-two")
	require.NoError(t, err)
	require.NotEqual(t, before, after)
	_, err = cfg.RecoveryTargetSHA256("")
	require.Error(t, err)
}

func TestRecoveryTargetDetectsBadgerDirectoryReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.Mkdir(path, 0o700))
	cfg := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: path}}
	before, err := cfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	require.NoError(t, os.Rename(path, path+"-original"))
	require.NoError(t, os.Mkdir(path, 0o700))
	after, err := cfg.RecoveryTargetSHA256("")
	require.NoError(t, err)
	require.NotEqual(t, before, after)
	cfg.Badger.InMemory = true
	_, err = cfg.RecoveryTargetSHA256("")
	require.Error(t, err)
}
