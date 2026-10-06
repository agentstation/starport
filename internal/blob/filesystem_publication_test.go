package blob_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/stretchr/testify/require"
)

type processPublicationReader struct {
	once   sync.Once
	reader io.Reader
}

func (r *processPublicationReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		_, _ = fmt.Fprintln(os.Stdout, "publication-paused")
		var resume [1]byte
		_, _ = io.ReadFull(os.Stdin, resume[:])
	})
	return r.reader.Read(p)
}
func TestFilesystemRetirementFencesAnotherProcess(t *testing.T) {
	if root := os.Getenv("STARPORT_BLOB_RETIREMENT_CHILD"); root != "" {
		store, err := blob.NewFilesystem(root)
		require.NoError(t, err)
		_, err = store.Publish(t.Context(), "process-output", &processPublicationReader{reader: strings.NewReader("delayed bytes")})
		require.ErrorIs(t, err, blob.ErrPublicationExists)
		return
	}
	root := t.TempDir()
	store, err := blob.NewFilesystem(root)
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestFilesystemRetirementFencesAnotherProcess$")
	child.Env = append(os.Environ(), "STARPORT_BLOB_RETIREMENT_CHILD="+root)
	stdout, err := child.StdoutPipe()
	require.NoError(t, err)
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	var diagnostics strings.Builder
	child.Stderr = &diagnostics
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "publication-paused\n", line)
	require.NoError(t, store.Retire(t.Context(), "process-output"))
	_, err = stdin.Write([]byte{1})
	require.NoError(t, err)
	require.NoError(t, stdin.Close())
	require.NoError(t, child.Wait(), diagnostics.String())
	reopened, err := blob.NewFilesystem(root)
	require.NoError(t, err)
	_, err = reopened.StatPublished(t.Context(), "process-output")
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = reopened.Publish(t.Context(), "process-output", strings.NewReader("new bytes"))
	require.ErrorIs(t, err, blob.ErrPublicationExists)
}

func TestRetireConcurrentlyOnOneKey(t *testing.T) {
	store, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	_, err = store.Publish(t.Context(), "shared", strings.NewReader("payload"))
	require.NoError(t, err)
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { errs[i] = store.Retire(t.Context(), "shared") })
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	_, err = store.StatPublished(t.Context(), "shared")
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = store.ReadPublished(t.Context(), "shared")
	require.ErrorIs(t, err, blob.ErrNotFound)
	_, err = store.Publish(t.Context(), "shared", strings.NewReader("payload"))
	require.ErrorIs(t, err, blob.ErrPublicationExists)
}
