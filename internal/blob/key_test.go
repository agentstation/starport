package blob_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
)

func TestValidateKey(t *testing.T) {
	t.Parallel()

	valid := []string{"a", "file-id", "file_id", "file.id", strings.Repeat("k", blob.MaxKeyLength)}
	for _, key := range valid {
		require.NoErrorf(t, blob.ValidateKey(key), "key %q", key)
	}

	invalid := map[string]string{
		"empty":              "",
		"forward separator":  "objects/one",
		"backward separator": `objects\one`,
		"current directory":  ".",
		"parent directory":   "..",
		"traversal":          "../../etc/passwd",
		"space":              "two words",
		"colon":              "drive:name",
		"null byte":          "one\x00two",
		"newline":            "one\ntwo",
		"too long":           strings.Repeat("k", blob.MaxKeyLength+1),
	}
	for name, key := range invalid {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, blob.ValidateKey(key), blob.ErrInvalidKey)
		})
	}
}

// TestFilesystemRejectsAPathSeparatorBeforeAnyWrite holds FIL-V02 against the
// medium. The contract test proves the error. This proves the timing: a
// refused key leaves the root exactly as it found it, so a traversal attempt
// cannot create a file outside the object tree and cannot leave a staged one
// inside it.
func TestFilesystemRejectsAPathSeparatorBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := blob.NewFilesystem(root)
	require.NoError(t, err)

	before := walk(t, root)

	for _, key := range []string{"objects/escape", `..\escape`, "../../escape"} {
		_, err := store.Put(context.Background(), key, strings.NewReader("payload"))
		require.ErrorIsf(t, err, blob.ErrInvalidKey, "put %q", key)
	}

	require.Equal(t, before, walk(t, root), "a refused key changed the root")

	// The parent of the root stays untouched as well, which is where a
	// traversal would land if the key reached a path join.
	entries, err := os.ReadDir(filepath.Dir(root))
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), "escape")
	}
}

// walk lists every path under root, relative to it, so two readings compare.
func walk(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, relative)
		return nil
	}))
	return paths
}

func TestNewFilesystemRefusesAnEmptyRoot(t *testing.T) {
	t.Parallel()

	_, err := blob.NewFilesystem("")
	require.Error(t, err)
}

// TestFilesystemStagesOutsideTheObjectTree proves the staging directory drains.
// A failed put that left its staged file behind would grow the root without
// bound, and no record would name the bytes to sweep.
func TestFilesystemStagesOutsideTheObjectTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := blob.NewFilesystem(root)
	require.NoError(t, err)

	_, err = store.Put(context.Background(), "staged", &errAfter{remaining: 4096, err: os.ErrClosed})
	require.Error(t, err)

	_, err = store.Put(context.Background(), "committed", strings.NewReader("value"))
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Join(root, "staging"))
	require.NoError(t, err)
	require.Empty(t, entries, "staging holds a file after both puts finished")
}
