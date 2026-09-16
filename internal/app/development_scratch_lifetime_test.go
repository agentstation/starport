package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const developmentScratchHelperEnvironment = "STARPORT_TEST_SCRATCH_DIRECTORY"

func TestDevelopmentScratchExcludesLiveProcessAndRecoversKilledProcess(t *testing.T) {
	temporary := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDevelopmentScratchProcess$")
	command.Env = append(os.Environ(), developmentScratchHelperEnvironment+"="+temporary)
	input, err := command.StdinPipe()
	require.NoError(t, err)
	output, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	waited := false
	t.Cleanup(func() {
		_ = input.Close()
		_ = command.Process.Kill()
		if !waited {
			_ = command.Wait()
		}
	})
	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())
	path := scanner.Text()
	require.Equal(t, temporary, filepath.Dir(path))
	require.FileExists(t, filepath.Join(path, "files", "owned"))
	report, err := recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.Equal(t, 1, report.Live)
	require.Zero(t, report.Recovered)
	require.DirExists(t, path)

	require.NoError(t, command.Process.Kill())
	require.Error(t, command.Wait())
	waited = true
	report, err = recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.Equal(t, 1, report.Recovered)
	require.Zero(t, report.Preserved)
	require.NoDirExists(t, path)
}

func TestDevelopmentScratchProcess(t *testing.T) {
	temporary := os.Getenv(developmentScratchHelperEnvironment)
	if temporary == "" {
		return
	}
	session, err := newPublishedDevelopmentScratch(t, temporary)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(session.path, "files", "owned"), []byte("ephemeral"), 0o600))
	_, err = fmt.Fprintln(os.Stdout, session.path)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestDevelopmentScratchPreservesUnknownEntries(t *testing.T) {
	for _, name := range []string{"operator", ".starport-development/operator", ".starport-development/.record-publications/operator"} {
		t.Run(name, func(t *testing.T) {
			temporary := t.TempDir()
			session, err := newPublishedDevelopmentScratch(t, temporary)
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.lock.Close() })
			path := filepath.Join(session.path, name)
			require.NoError(t, os.WriteFile(path, nil, 0o600))
			require.Error(t, session.close())
			require.FileExists(t, path)
			report, err := recoverDevelopmentScratch(t.Context(), temporary)
			require.NoError(t, err)
			require.Equal(t, 1, report.Preserved)
			require.Zero(t, report.Recovered)
			require.FileExists(t, path)
		})
	}
}

func TestDevelopmentScratchPreservesChangedOwnershipRecord(t *testing.T) {
	temporary := t.TempDir()
	session, err := newPublishedDevelopmentScratch(t, temporary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.lock.Close() })
	path := filepath.Join(session.path, developmentScratchOwner, developmentScratchRecordName)
	require.NoError(t, os.WriteFile(path, append(session.encoded, '\n'), 0o600))
	require.Error(t, session.close())
	report, err := recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.Equal(t, 1, report.Preserved)
	require.Zero(t, report.Recovered)
	require.DirExists(t, session.path)
}

func TestDevelopmentScratchReplacedLockCannotAuthorizeRecovery(t *testing.T) {
	temporary := t.TempDir()
	session, err := newPublishedDevelopmentScratch(t, temporary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.lock.Close() })
	path := filepath.Join(session.path, developmentScratchOwner, developmentScratchLock)
	if err := os.Rename(path, path+".old"); err != nil {
		report, recoveryErr := recoverDevelopmentScratch(t.Context(), temporary)
		require.NoError(t, recoveryErr)
		require.Equal(t, 1, report.Live, "native lock exclusion must preserve the active session")
		require.NoError(t, session.close())
		return
	}
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	report, err := recoverDevelopmentScratch(t.Context(), temporary)
	require.NoError(t, err)
	require.Equal(t, 1, report.Preserved)
	require.Zero(t, report.Recovered)
	require.DirExists(t, session.path)
	require.Error(t, session.close())
}

func TestDevelopmentScratchPreservesReplacedRoot(t *testing.T) {
	temporary := t.TempDir()
	session, err := newPublishedDevelopmentScratch(t, temporary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.lock.Close() })
	if err := os.Rename(session.path, session.path+"-moved"); err != nil {
		require.NoError(t, session.close())
		return
	}
	require.NoError(t, os.Mkdir(session.path, developmentScratchPermissions))
	path := filepath.Join(session.path, "operator")
	require.NoError(t, os.WriteFile(path, []byte("preserve"), 0o600))
	require.Error(t, session.close())
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(contents))
}

func TestDevConstructorFailureRemovesOwnedScratch(t *testing.T) {
	temporary := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temporary)
	}
	cfg := validDevelopmentConfig(t)
	cfg.Server.Port = -1
	runtime, err := NewDevelopment(t.Context(), cfg)
	require.Error(t, err)
	require.Nil(t, runtime)
	entries, err := os.ReadDir(temporary)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), developmentScratchPrefix)
	}
}
