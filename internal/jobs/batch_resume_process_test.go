package jobs_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type resumeProcessFiles struct{ resultLossIO }

func (f resumeProcessFiles) OpenInput(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("{}\n{}\n{}\n")), nil
}

func TestBatchResumeAfterProcessLoss(t *testing.T) {
	const childKey = "STARPORT_BATCH_RESUME_CHILD"
	openStore := func(root string) (storage.KVStore, jobs.BatchRepository, resumeProcessFiles) {
		kv, err := storage.OpenBadger(storage.BadgerConfig{Path: filepath.Join(root, "kv"), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, kv.Close()) })
		records, err := jobs.OpenBatchRepository(kv)
		require.NoError(t, err)
		fileRecords, err := files.OpenRepository(kv)
		require.NoError(t, err)
		blobs, err := blob.NewFilesystem(filepath.Join(root, "blobs"))
		require.NoError(t, err)
		fileService, err := files.NewService(fileRecords, blobs)
		require.NoError(t, err)
		return kv, records, resumeProcessFiles{resultLossIO{fileService}}
	}
	if root := os.Getenv(childKey); root != "" {
		_, records, fileIO := openStore(root)
		service, err := jobs.NewBatchService(records, jobs.WithBatchConcurrency(1))
		require.NoError(t, err)
		_, err = service.Submit(t.Context(), jobs.BatchSubmission{ID: "resume-process", Account: "account", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: fileIO, Runner: resultLossRunner{filepath.Join(root, "marker")}})
		require.NoError(t, err)
		select {}
	}
	root := t.TempDir()
	var output bytes.Buffer
	command := exec.Command(os.Args[0], "-test.run=^TestBatchResumeAfterProcessLoss$", "-test.timeout=30s")
	command.Env = append(os.Environ(), childKey+"="+root)
	command.Stdout = &output
	command.Stderr = &output
	require.NoError(t, command.Start())
	t.Cleanup(func() { _ = command.Process.Kill() })
	ready := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, "marker")); err == nil {
			ready = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, command.Process.Kill())
	require.Error(t, command.Wait())
	require.True(t, ready, "child did not reach uncertain second line: %s", output.String())
	_, records, fileIO := openStore(root)
	first, err := records.ReadLine(t.Context(), "account", "resume-process", 1)
	require.NoError(t, err)
	require.True(t, first.ResultReady)
	second, err := records.ReadLine(t.Context(), "account", "resume-process", 2)
	require.NoError(t, err)
	require.False(t, second.ResultReady)
	runner := &echoRunner{}
	resumed, err := jobs.NewBatchService(records, jobs.WithBatchFiles(func(jobs.Batch) jobs.BatchIO { return fileIO }), jobs.WithBatchRecoveryRunner(func(context.Context, jobs.Batch) (jobs.LineRunner, error) { return runner, nil }))
	require.NoError(t, err)
	_, err = resumed.Sweep(t.Context())
	require.NoError(t, err)
	require.Equal(t, []int{3}, runner.ranLines())
	for number, before := range map[int]jobs.BatchLine{1: first, 2: second} {
		after, err := records.ReadLine(t.Context(), "account", "resume-process", number)
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
	third, err := records.ReadLine(t.Context(), "account", "resume-process", 3)
	require.NoError(t, err)
	require.True(t, third.ResultReady)
	retained, err := fileIO.OpenResult(t.Context(), first.OutputFileID)
	require.NoError(t, err)
	body, err := io.ReadAll(retained)
	require.NoError(t, err)
	require.NoError(t, retained.Close())
	require.Contains(t, string(body), "completed-line-one")
	batch, err := resumed.Get(t.Context(), "account", "resume-process")
	require.NoError(t, err)
	require.False(t, batch.RunFinished)
	_, err = resumed.Sweep(t.Context())
	require.NoError(t, err)
	require.Equal(t, []int{3}, runner.ranLines())
	require.NoError(t, resumed.Close(t.Context()))
}
