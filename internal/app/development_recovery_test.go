package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestDevelopmentScratchPreservesReplacedChild(t *testing.T) {
	temporary := t.TempDir()
	session, err := newPublishedDevelopmentScratch(t, temporary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.lock.Close() })
	path := filepath.Join(session.path, "files")
	require.NoError(t, os.Rename(path, filepath.Join(temporary, "original-files")))
	require.NoError(t, os.Mkdir(path, developmentScratchPermissions))
	sentinel := filepath.Join(path, "operator")
	require.NoError(t, os.WriteFile(sentinel, []byte("preserve"), 0o600))
	require.Error(t, session.close())
	report, err := recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.Equal(t, []string{session.path}, report.PreservedPaths)
	require.Zero(t, report.Recovered)
	contents, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(contents))
}

func TestDevelopmentScratchRecoversInterruptedCleanup(t *testing.T) {
	temporary := t.TempDir()
	session, err := newPublishedDevelopmentScratch(t, temporary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.lock.Close() })
	require.NoError(t, os.Remove(filepath.Join(session.path, "files")))
	require.NoError(t, session.lock.Close())
	report, err := recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.Equal(t, 1, report.Recovered)
	require.Zero(t, report.Preserved)
	require.NoDirExists(t, session.path)
}

func TestDevelopmentScratchPreservesUnrecognizedRecords(t *testing.T) {
	for _, value := range []string{"{", "{}", strings.Repeat("x", developmentScratchRecordLimit+1)} {
		t.Run(fmt.Sprintf("bytes-%d", len(value)), func(t *testing.T) {
			temporary := t.TempDir()
			session, err := newPublishedDevelopmentScratch(t, temporary)
			require.NoError(t, err)
			require.NoError(t, session.lock.Close())
			path := filepath.Join(session.path, developmentScratchOwner, developmentScratchRecordName)
			require.NoError(t, os.WriteFile(path, []byte(value), 0o600))
			report, err := recoverDevelopmentScratch(t.Context(), temporary)
			require.NoError(t, err)
			require.Equal(t, []string{session.path}, report.PreservedPaths)
			require.Zero(t, report.Recovered)
			contents, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, value, string(contents))
		})
	}
}

func TestDevelopmentScratchRecoveryBoundsCandidates(t *testing.T) {
	temporary := t.TempDir()
	for index := range developmentRecoverySessionLimit + 1 {
		require.NoError(t, os.Mkdir(filepath.Join(temporary, fmt.Sprintf("%s%d", developmentScratchPrefix, index)), developmentScratchPermissions))
	}
	report, err := recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.True(t, report.Limited)
	require.Equal(t, developmentRecoverySessionLimit, report.Preserved)
	require.Len(t, report.PreservedPaths, developmentRecoverySessionLimit)
	entries, err := os.ReadDir(temporary)
	require.NoError(t, err)
	require.Len(t, entries, developmentRecoverySessionLimit+1)
}

func TestDevelopmentScratchCancelledRecoveryPreservesState(t *testing.T) {
	temporary := t.TempDir()
	session, err := newPublishedDevelopmentScratch(t, temporary)
	require.NoError(t, err)
	require.NoError(t, session.lock.Close())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	report, err := recoverDevelopmentScratch(ctx, temporary)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, report.Recovered)
	require.DirExists(t, session.path)
	report, err = recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.Equal(t, 1, report.Recovered)
}

func TestDevFailedCompositionPreservesUnpublishedScratch(t *testing.T) {
	temporary := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temporary)
	}
	cfg := validDevelopmentConfig(t)
	cause := errors.New("composition failed")
	runtime, err := NewDevelopment(t.Context(), cfg, func(options *buildOptions) {
		options.factories.openSQL = func(config.StorageConfig) (*sqlstore.DB, error) { return nil, cause }
	})
	require.ErrorIs(t, err, cause)
	require.Nil(t, runtime)
	path := filepath.Dir(cfg.Files.Path)
	require.DirExists(t, path, "failed composition cannot prove that all resources stopped")
	report, err := recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.Contains(t, report.PreservedPaths, path)
	require.Zero(t, report.Recovered)
}
