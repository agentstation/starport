//go:build !windows

package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/stretchr/testify/require"
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

	result, err := source.PublishFileTree(t.Context(), request, func(context.Context, string) error {
		moved := filepath.Join(base, "moved-parent")
		require.NoError(t, os.Rename(parent, moved))
		_, err := productfiles.CreateDirectory(parent)
		require.NoError(t, err)
		// Preserve the leaf identity while replacing the parent that the retry must sync.
		require.NoError(t, os.Rename(filepath.Join(moved, "restored"), request.Destination))
		return nil
	})
	require.Error(t, err)
	require.False(t, result.Reused)
	require.False(t, result.Published)
	require.FileExists(t, filepath.Join(request.Destination, "config.env"))
}
