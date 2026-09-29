package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRestoreMetadataReadsOnlyVerifiedBoundedBytes(t *testing.T) {
	source, request, directory := backupBundleFixture(t)
	manifest, err := BackupBundle(t.Context(), directory, source, request)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: directory, ManifestSHA256: digest}, source.Encryption)
	require.NoError(t, err)
	id := "configuration/config.env"
	expected, err := os.ReadFile(filepath.Join(directory, "files", filepath.FromSlash(id)))
	require.NoError(t, err)
	actual, err := verified.SelectedFile(t.Context(), id, int64(len(expected)))
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	for _, limit := range []int64{-1, 0, int64(len(expected) - 1), bundleMaxManifestBytes + 1} {
		_, err := verified.SelectedFile(t.Context(), id, limit)
		require.Error(t, err)
	}
	for _, name := range []string{"../escape", "configuration/missing", "/etc/passwd"} {
		_, err := verified.SelectedFile(t.Context(), name, bundleMaxManifestBytes)
		require.Error(t, err)
	}
	artifacts := verified.SelectedFileArtifacts()
	require.Equal(t, int64(len(expected)), artifacts[id].Size)
	require.Equal(t, verified.SelectedFileHashes()[id], artifacts[id].SHA256)
	delete(artifacts, id)
	require.Contains(t, verified.SelectedFileArtifacts(), id)
	hashes := verified.SelectedFileHashes()
	hashes[id] = "changed by caller"
	require.NotEqual(t, hashes, verified.SelectedFileHashes())
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = verified.SelectedFile(canceled, id, bundleMaxManifestBytes)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "files", filepath.FromSlash(id)), []byte("changed after verification"), 0o600))
	_, err = verified.SelectedFile(t.Context(), id, bundleMaxManifestBytes)
	require.ErrorContains(t, err, "changed after verification")
	var absent *RestoreSource
	_, err = absent.SelectedFile(t.Context(), id, bundleMaxManifestBytes)
	require.Error(t, err)
	require.Empty(t, absent.SelectedFileHashes())
	require.Empty(t, absent.SelectedFileArtifacts())
}
