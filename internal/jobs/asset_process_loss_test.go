package jobs_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type stopAfterAssetPublication struct {
	blob.Store
	count, stopAt int
	marker        string
}

func (b *stopAfterAssetPublication) Publish(ctx context.Context, key string, r io.Reader) (blob.Info, error) {
	info, err := b.Store.Publish(ctx, key, r)
	if err != nil {
		return info, err
	}
	b.count++
	if b.count == b.stopAt {
		if err := os.WriteFile(b.marker+".tmp", []byte(key), 0600); err != nil {
			return blob.Info{}, err
		}
		if err := os.Rename(b.marker+".tmp", b.marker); err != nil {
			return blob.Info{}, err
		}
		select {}
	}
	return info, nil
}

func TestVideoBytesSurviveProcessLoss(t *testing.T) {
	const childRoot = "STARPORT_VIDEO_PUBLICATION_CHILD"
	const childMode = "STARPORT_VIDEO_PUBLICATION_MODE"
	open := func(t *testing.T, root string) (jobs.Repository, blob.Store) {
		t.Helper()
		kv, err := storage.OpenBadger(storage.BadgerConfig{Path: filepath.Join(root, "kv"), SyncWrites: true, Compression: "snappy", NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, kv.Close()) })
		records, err := jobs.OpenRepository(kv)
		require.NoError(t, err)
		medium, err := blob.NewFilesystem(filepath.Join(root, "blobs"))
		require.NoError(t, err)
		return records, medium
	}
	if root := os.Getenv(childRoot); root != "" {
		records, medium := open(t, root)
		mode := os.Getenv(childMode)
		stopped := &stopAfterAssetPublication{Store: medium, stopAt: 1, marker: filepath.Join(root, "marker")}
		if mode == "native-asset" {
			stopped.stopAt = 2
		}
		service, err := jobs.NewService(records, jobs.WithAssetStore(stopped), jobs.WithClock(func() time.Time { return submitted }))
		require.NoError(t, err)
		if mode == "provider-asset" {
			runner := finishingRunner()
			job, err := service.Submit(t.Context(), func(context.Context) (jobs.Runner, error) { return runner, nil }, submissionFor(accountA))
			require.NoError(t, err)
			_, err = service.Refresh(t.Context(), runner, accountA, job.ID)
			require.NoError(t, err)
		} else {
			runner := nativeFixture()
			_, err = service.Submit(t.Context(), func(context.Context) (jobs.Runner, error) { return runner, nil }, submissionFor(accountA))
			require.NoError(t, err)
		}
		t.Fatal("publication barrier did not stop the child")
	}
	for _, mode := range []string{"native-receipt", "native-asset", "provider-asset"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			command := exec.Command(os.Args[0], "-test.run=^TestVideoBytesSurviveProcessLoss$", "-test.timeout=20s")
			command.Env = append(os.Environ(), childRoot+"="+root, childMode+"="+mode)
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			require.NoError(t, command.Start())
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})
			ready := false
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(filepath.Join(root, "marker")); err == nil {
					ready = true
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			require.NoError(t, command.Process.Kill())
			require.Error(t, command.Wait())
			require.True(t, ready, "child did not reach publication: %s", output.String())
			key, err := os.ReadFile(filepath.Join(root, "marker"))
			require.NoError(t, err)
			records, medium := open(t, root)
			// No runner or external downloader exists in this recovery process.
			service, err := jobs.NewService(records, jobs.WithAssetStore(medium), jobs.WithClock(func() time.Time { return submitted }))
			require.NoError(t, err)
			_, err = service.Sweep(t.Context())
			require.NoError(t, err)
			all, err := records.List(t.Context(), accountA, 10)
			require.NoError(t, err)
			require.Len(t, all, 1)
			job, reader, err := service.Open(t.Context(), accountA, all[0].ID)
			require.NoError(t, err)
			data, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			expected := "measured-native-video"
			if mode == "provider-asset" {
				expected = "finished-video-bytes"
			}
			require.Equal(t, expected, string(data))
			if mode != "native-receipt" {
				require.Equal(t, string(key), job.AssetKey)
			}
		})
	}
}
