package recovery

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRestoreCatalogSourceKeepsOriginalSnapshotAndPayloadBounds(t *testing.T) {
	source, request, directory := backupBundleFixture(t)
	large := bytes.Repeat([]byte("x"), bundleMaxManifestBytes+1)
	payload := filepath.Join(t.TempDir(), "original-baseline")
	require.NoError(t, os.WriteFile(payload, large, 0600))
	source.Files = append(source.Files, BundleFile{ID: "catalog/baseline", Path: payload})
	manifest, err := BackupBundle(t.Context(), directory, source, request)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: directory, ManifestSHA256: digest}, source.Encryption)
	require.NoError(t, err)
	require.Equal(t, request.Boundary, verified.CapturedBoundary())
	require.Equal(t, digest, verified.ManifestDigest())
	view, err := verified.OpenCapturedKV(t.Context())
	require.NoError(t, err)
	require.NoError(t, view.Enumerate(t.Context(), func(record storage.TransferRecord) error { return nil }))
	require.NoError(t, view.Close())
	actual, err := verified.SelectedPayload(t.Context(), "catalog/baseline", int64(len(large)))
	require.NoError(t, err)
	require.Equal(t, large, actual)
	actual, err = verified.SelectedPayload(t.Context(), "catalog/baseline", 2*catalogstorage.DefaultRetentionInputMaxBytes)
	require.NoError(t, err, "existing encoded retention envelopes have a separate 512 MiB bound")
	require.Equal(t, large, actual)
	_, err = verified.SelectedFile(t.Context(), "catalog/baseline", int64(len(large)))
	require.Error(t, err, "metadata bound stays unchanged")
	for _, limit := range []int64{0, -1, int64(len(large) - 1), 2*catalogstorage.DefaultRetentionInputMaxBytes + 1} {
		_, err = verified.SelectedPayload(t.Context(), "catalog/baseline", limit)
		require.Error(t, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = verified.OpenCapturedKV(canceled)
	require.ErrorIs(t, err, context.Canceled)
	_, err = verified.SelectedPayload(canceled, "catalog/baseline", int64(len(large)))
	require.ErrorIs(t, err, context.Canceled)
	_, err = verified.SelectedPayload(nil, "catalog/baseline", int64(len(large)))
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "files/catalog/baseline"), bytes.Repeat([]byte("y"), len(large)), 0600))
	_, err = verified.SelectedPayload(t.Context(), "catalog/baseline", int64(len(large)))
	require.ErrorContains(t, err, "changed after verification")
	require.NoError(t, os.WriteFile(filepath.Join(directory, bundleKVFile), []byte("changed snapshot"), 0600))
	_, err = verified.OpenCapturedKV(t.Context())
	require.Error(t, err)
	var absent *RestoreSource
	require.Empty(t, absent.ManifestDigest())
	require.Equal(t, Record{}, absent.CapturedBoundary())
	_, err = absent.OpenCapturedKV(t.Context())
	require.Error(t, err)
	_, err = absent.SelectedPayload(t.Context(), "catalog/baseline", 1)
	require.Error(t, err)
	_, err = verified.OpenCapturedKV(nil)
	require.Error(t, err)
}
