package blob

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilesystemRestoreRetryPreservesBarrierAndVerifiesContents(t *testing.T) {
	source, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
	require.NoError(t, err)
	seedSnapshot(t, source)
	archive := filepath.Join(snapshotDirectory(t), "snapshot.tar")
	receipt, err := Backup(t.Context(), source, archive)
	require.NoError(t, err)
	destination := filepath.Join(snapshotDirectory(t), "target")
	_, err = RestoreFilesystemOnce(t.Context(), destination, archive, "operation", receipt)
	require.NoError(t, err)
	_, err = RestoreFilesystemOnce(t.Context(), destination, archive, "operation", receipt)
	require.NoError(t, err)
	_, err = NewFilesystem(destination)
	require.ErrorIs(t, err, ErrImportRestricted)
	_, err = RestoreFilesystemOnce(t.Context(), destination, archive, "different", receipt)
	require.Error(t, err)
	// Same-length content changes must not pass a retained receipt check.
	name := filepath.Join(destination, filepath.FromSlash(blobAddress(objectsDir, "mutable")))
	require.NoError(t, os.WriteFile(name, []byte("changed bytes"), 0600))
	_, err = RestoreFilesystemOnce(t.Context(), destination, archive, "operation", receipt)
	require.Error(t, err)
}
