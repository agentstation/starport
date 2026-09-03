package catalog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

// identityTestSettings returns settings that open one embedded runtime over a
// named state directory, workspace, and listen address.
func identityTestSettings(state, workspace, address string) Settings {
	return Settings{
		Source:              string(starmap.SourceEmbedded),
		SourceStartupPolicy: string(starmap.StartupPreferLocal),
		SourcePollInterval:  time.Hour,
		SourceMaxHops:       8,
		TransferIdleTimeout: time.Minute,
		TransferMaxDuration: time.Minute,
		WorkspacePath:       workspace,
		StateDirectory:      state,
		ListenAddress:       address,
	}
}

// instanceIdentity opens one runtime over the settings, reads the instance
// identity the connected runtime derived, and closes the runtime.
func instanceIdentity(t *testing.T, settings Settings) string {
	t.Helper()
	runtime, err := openRuntime(
		t.Context(), storage.NewMockStore(), settings, nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(t.Context())) })
	return runtime.Status().InstanceIdentity
}

// TestListenAddressSeparatesTwoInstances proves the listen address reaches the
// connected runtime options. Two processes that share one host and one state
// root derive two instance identities, so the runtime lease still fences a
// durable commit.
func TestListenAddressSeparatesTwoInstances(t *testing.T) {
	state := t.TempDir()
	workspace := t.TempDir()

	first := instanceIdentity(t, identityTestSettings(state, workspace, "127.0.0.1:8080"))
	second := instanceIdentity(t, identityTestSettings(state, workspace, "127.0.0.1:9090"))
	repeat := instanceIdentity(t, identityTestSettings(state, workspace, "127.0.0.1:8080"))

	require.NotEmpty(t, first)
	require.NotEmpty(t, second)
	assert.NotEqual(t, first, second)
	assert.Equal(t, first, repeat)
}

// TestStateDirectoryIsNeverTheWorkspacePath proves the two directories stay
// apart. The connected runtime writes its identity seed and its layers under
// the state directory alone, so a workspace an operator shares between two
// instances carries no identity.
func TestStateDirectoryIsNeverTheWorkspacePath(t *testing.T) {
	state := t.TempDir()
	workspace := t.TempDir()

	identity := instanceIdentity(t, identityTestSettings(state, workspace, "127.0.0.1:8080"))
	require.NotEmpty(t, identity)

	stateEntries, err := entryNames(state)
	require.NoError(t, err)
	assert.NotEmpty(t, stateEntries, "the state directory holds no runtime state")

	workspaceEntries, err := entryNames(workspace)
	require.NoError(t, err)
	for _, name := range stateEntries {
		assert.NotContains(
			t, workspaceEntries, name,
			"the workspace at %s holds runtime state", filepath.Join(workspace, name),
		)
	}
}

// entryNames returns the names one directory holds.
func entryNames(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}
