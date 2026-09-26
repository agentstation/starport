//go:build !windows

package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func grantPrivateStatePublicRead(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.Chmod(path, 0o755))
}
