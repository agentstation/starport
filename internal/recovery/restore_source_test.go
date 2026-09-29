package recovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifiedRestoreSourceRechecksChangedSQLBytes(t *testing.T) {
	source, request, directory := backupBundleFixture(t)
	manifest, err := BackupBundle(t.Context(), directory, source, request)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: directory, ManifestSHA256: digest}, source.Encryption)
	require.NoError(t, err)
	require.Equal(t, request.Boundary.DeploymentID, verified.DeploymentID())
	require.NoError(t, os.WriteFile(filepath.Join(directory, bundleSQLFile), []byte("changed after inspection"), 0o600))
	target, _, _ := bundleRestoreTargets(t, false)
	_, err = verified.Prepare(t.Context(), target, RestoreOperation{ID: "restore", FencingEvidence: "proof"})
	require.Error(t, err)
	require.NoError(t, target.SQL.CheckImportBarrier(t.Context()))
	require.NoDirExists(t, target.FilesDirectory)
}
