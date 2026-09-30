//go:build linux || darwin

package recovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetainedActivationHistoryRefusesNonprivatePOSIXEvidence(t *testing.T) {
	f, _, _, request := retainedActivationFixture(t, false)
	for _, path := range []string{request.Directory, filepath.Join(request.Directory, closedFinalHistoryFile), filepath.Join(f.source.request.Directory, bundleManifestFile)} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			information, err := os.Stat(path)
			require.NoError(t, err)
			original := information.Mode().Perm()
			require.NoError(t, os.Chmod(path, original|0o040))
			_, err = OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
			require.Error(t, err)
			actual, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, original|0o040, actual.Mode().Perm())
			require.NoError(t, os.Chmod(path, original))
		})
	}
	_, err := OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
	require.NoError(t, err)
}
