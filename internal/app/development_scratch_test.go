package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const developmentScratchPermissions = 0o700

func TestDevRepeatedClosePreservesReusedScratchPath(t *testing.T) {
	runtime, err := NewDevelopment(t.Context(), validDevelopmentConfig(t))
	require.NoError(t, err)
	scratch := runtime.scratchRoot
	require.NoError(t, runtime.Close(t.Context()))
	require.NoDirExists(t, scratch)
	require.NoError(t, os.Mkdir(scratch, developmentScratchPermissions))
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(scratch)) })
	sentinel := filepath.Join(scratch, "operator.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("replacement data"), 0o600))

	require.NoError(t, runtime.Close(t.Context()))
	contents, err := os.ReadFile(sentinel)
	require.NoError(t, err, "repeated shutdown must preserve a replacement directory")
	require.Equal(t, "replacement data", string(contents))
}

func newPublishedDevelopmentScratch(t testing.TB, temporary string) (*developmentScratch, error) {
	t.Helper()
	session, err := newDevelopmentScratch(t.Context(), temporary)
	if err != nil {
		return nil, err
	}
	if err := session.publish(t.Context()); err != nil {
		_ = session.lock.Close()
		return nil, err
	}
	return session, nil
}
