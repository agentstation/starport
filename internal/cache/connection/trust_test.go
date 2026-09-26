package connection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrustBundleRefusals(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name string
		data string
	}{
		{"empty", ""}, {"malformed", "private-value"}, {"oversized", strings.Repeat("x", maxTrustBundleBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name)
			require.NoError(t, os.WriteFile(path, []byte(tc.data), 0o600))
			_, err := LoadRoots(path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), path)
			require.NotContains(t, err.Error(), "private-value")
		})
	}
	for _, path := range []string{"relative.pem", root, filepath.Join(root, "missing")} {
		_, err := LoadRoots(path)
		require.Error(t, err)
		require.NotContains(t, err.Error(), root)
	}
}
