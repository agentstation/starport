package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotViewReadsVerifiedBytesWithoutExposingWrites(t *testing.T) {
	source, err := NewFilesystem(filepath.Join(snapshotDirectory(t), "source"))
	require.NoError(t, err)
	_, err = source.Publish(t.Context(), "live", strings.NewReader("bytes"))
	require.NoError(t, err)
	require.NoError(t, source.Retire(t.Context(), "retired"))
	archive := filepath.Join(snapshotDirectory(t), "archive.tar")
	receipt, err := Backup(t.Context(), source, archive)
	require.NoError(t, err)
	scratch := snapshotDirectory(t)
	view, err := OpenSnapshot(t.Context(), archive, scratch, receipt)
	require.NoError(t, err)
	_, writable := view.(Store)
	require.False(t, writable)
	digest := sha256.Sum256([]byte("bytes"))
	require.NoError(t, VerifyPublished(t.Context(), view, "live", 5, hex.EncodeToString(digest[:])))
	require.ErrorIs(t, VerifyPublished(t.Context(), view, "live", 4, ""), ErrCorruptPublication)
	require.ErrorIs(t, VerifyPublished(t.Context(), view, "live", 6, ""), ErrCorruptPublication)
	require.ErrorIs(t, VerifyPublished(t.Context(), view, "live", 5, strings.Repeat("0", 64)), ErrCorruptPublication)
	require.ErrorIs(t, VerifyPublished(t.Context(), view, "retired", 0, ""), ErrNotFound)
	require.NoError(t, view.Close())
	contents, err := os.ReadDir(scratch)
	require.NoError(t, err)
	require.Empty(t, contents)
	receipt.SHA256 = strings.Repeat("0", 64)
	view, err = OpenSnapshot(t.Context(), archive, scratch, receipt)
	require.Error(t, err)
	require.Nil(t, view)
}
