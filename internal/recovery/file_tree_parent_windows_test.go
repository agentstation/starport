package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestRestoreFileTreeRetryRefusesReplacedParent(t *testing.T) {
	source, request := restoreFileTreeFixture(t)
	base := privateKVDirectory(t)
	parent := filepath.Join(base, "parent")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	request.Destination = filepath.Join(parent, "restored")
	first, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
	require.NoError(t, err)
	require.True(t, first.Published)

	moved := filepath.Join(base, "moved-parent")
	checked := false
	result, err := source.PublishFileTree(t.Context(), request, func(context.Context, string) error {
		// The open parent handle prevents replacement until validation and sync finish.
		require.ErrorIs(t, os.Rename(parent, moved), windows.ERROR_SHARING_VIOLATION)
		require.NoDirExists(t, moved)
		checked = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, checked)
	require.True(t, result.Reused)
	require.False(t, result.Published)
	require.Equal(t, first.DirectoryIdentity, result.DirectoryIdentity)
	body, err := os.ReadFile(filepath.Join(request.Destination, "config.env"))
	require.NoError(t, err)
	expected, err := os.ReadFile(filepath.Join(source.request.Directory, "files/configuration/config.env"))
	require.NoError(t, err)
	require.Equal(t, expected, body)

	// The handle must release after the operation.
	require.NoError(t, os.Rename(parent, moved))
	require.FileExists(t, filepath.Join(moved, "restored", "config.env"))
}
