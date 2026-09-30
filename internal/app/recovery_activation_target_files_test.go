package app

import (
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRecoveryTargetCensusBindsNativeFilesAroundOwnerValidation(t *testing.T) {
	for _, change := range []string{"content", "identity", "added", "removed", "root"} {
		t.Run(change, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "selected")
			require.NoError(t, os.Mkdir(path, 0700))
			file := filepath.Join(path, "record.json")
			require.NoError(t, os.WriteFile(file, []byte("original"), 0600))
			_, _, err := inspectTopologyTree(t.Context(), path, func(*os.Root) error {
				switch change {
				case "content":
					return os.WriteFile(file, []byte("changed"), 0600)
				case "identity":
					if err := os.Rename(file, file+"-old"); err != nil {
						return err
					}
					return os.WriteFile(file, []byte("original"), 0600)
				case "added":
					return os.WriteFile(filepath.Join(path, "added"), []byte("added"), 0600)
				case "removed":
					return os.Remove(file)
				case "root":
					if err := os.Rename(path, path+"-old"); err != nil {
						return err
					}
					return os.Mkdir(path, 0700)
				}
				return nil
			}, false)
			require.ErrorIs(t, err, recovery.ErrConflict)
		})
	}
	path := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(path, "record"), []byte("original"), 0600))
	calls := 0
	first, files, err := inspectTopologyTree(t.Context(), path, func(*os.Root) error { calls++; return nil }, false)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Len(t, files, 1)
	second, again, err := inspectTopologyTree(t.Context(), path, func(*os.Root) error { return nil }, false)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, files, again)
}

func TestRecoveryTargetCensusRejectsOversizedOrLinkedEntries(t *testing.T) {
	for _, kind := range []string{"oversized", "symlink", "entry-count"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			name := filepath.Join(path, "record")
			switch kind {
			case "oversized":
				file, err := os.Create(name)
				require.NoError(t, err)
				require.NoError(t, file.Truncate((64<<20)+1))
				require.NoError(t, file.Close())
			case "symlink":
				require.NoError(t, os.Symlink(filepath.Join(path, "absent"), name))
			case "entry-count":
				for n := 0; n < 4097; n++ {
					require.NoError(t, os.Mkdir(filepath.Join(path, strconv.Itoa(n)), 0700))
				}
			}
			_, _, err := inspectTopologyTree(t.Context(), path, func(*os.Root) error { t.Fatal("unbounded or linked census reached semantic owner"); return nil }, false)
			require.Error(t, err)
		})
	}
}
