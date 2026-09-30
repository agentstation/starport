package catalog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
	"github.com/stretchr/testify/require"
)

func TestRecoveryTopologyTargetPreservesCanonicalPassivePolicy(t *testing.T) {
	for _, source := range []string{"embedded", "github", "starmap", "file"} {
		t.Run(source, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "uncreated-state")
			settings := identityTestSettings(directory, "", "127.0.0.1:8000")
			settings.Source = source
			settings.SourceURL = "https://authority.example"
			if source == "file" {
				settings.SourceURL = filepath.Join(t.TempDir(), "unread-catalog")
			}
			settings.Values = map[string]string{catalogconfig.SchedulerIdentity: "restore-owner", catalogconfig.NetworkMode: "offline"}
			settings.DeploymentID, settings.InstanceID = "destination", "replica"
			target, options, err := settings.RecoveryTopologyTarget()
			require.NoError(t, err)
			require.Equal(t, directory, target.Directory)
			require.Equal(t, "restore-owner", target.SchedulerIdentity)
			require.Equal(t, settings.directoryOwner(), target.Owner)
			policy, err := runtime.ResolveSourcePolicy(options...)
			require.NoError(t, err)
			require.Equal(t, runtime.SourceKind(source), policy.Kind)
			require.Equal(t, time.Hour, policy.PollInterval)
			_, err = os.Stat(directory)
			require.True(t, os.IsNotExist(err), "passive options created runtime state")
		})
	}
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "")
	settings.Values = map[string]string{catalogconfig.NetworkMode: "invalid"}
	_, _, err := settings.RecoveryTopologyTarget()
	require.Error(t, err)
	settings.Values = nil
	settings.DeploymentID = "invalid\x00owner"
	_, _, err = settings.RecoveryTopologyTarget()
	require.Error(t, err)
}
