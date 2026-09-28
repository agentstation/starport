package jobs_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/jobs/fileio"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
)

const aggregateSharedChild = "STARPORT_SHARED_AGGREGATE_CHILD"

type aggregateSharedConfig struct {
	URL, Namespace, Endpoint, Bucket, Marker, Stage string
}

func openSharedAggregate(t *testing.T, cfg aggregateSharedConfig) (storage.KVStore, blob.Store) {
	t.Helper()
	raw, err := storage.OpenValkey(storage.ValkeyConfig{URL: cfg.URL, DeploymentID: "aggregate-recovery-test", AllowInsecure: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, raw.Close()) })
	assets, err := blob.NewObjectStore(t.Context(), blob.ObjectStoreOptions{Endpoint: cfg.Endpoint, Region: "us-east-1", Bucket: cfg.Bucket, AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"})
	require.NoError(t, err)
	return repotest.NamespacedStore(raw, cfg.Namespace), assets
}

func TestBatchAggregateSharedProcessRecovery(t *testing.T) {
	if input := os.Getenv(aggregateSharedChild); input != "" {
		var cfg aggregateSharedConfig
		require.NoError(t, json.Unmarshal([]byte(input), &cfg))
		kv, assets := openSharedAggregate(t, cfg)
		records, err := jobs.OpenBatchRepository(kv)
		require.NoError(t, err)
		fileRecords, err := files.OpenRepository(kv)
		require.NoError(t, err)
		meter, err := storedbytes.NewStorageMeter(kv)
		require.NoError(t, err)
		fs, err := files.NewService(fileRecords, assets, files.WithMeter(meter))
		require.NoError(t, err)
		adapter := fileio.Store{Files: fs, Account: "a"}
		var output jobs.BatchIO = adapter
		if cfg.Stage == "publication" {
			output = aggregateProcessFiles{Store: adapter, marker: cfg.Marker}
		} else if cfg.Stage == "metadata" {
			records = aggregateProcessRepository{BatchRepository: records, marker: cfg.Marker}
		}
		service, err := jobs.NewBatchService(records)
		require.NoError(t, err)
		if cfg.Stage == "recover" {
			// Concurrent checkpoint retirement may require another bounded pass.
			require.Eventually(t, func() bool {
				final, err := service.RecoverResults(t.Context(), "a", "retained", output)
				return err == nil && final.ResultsReleased
			}, 10*time.Second, 10*time.Millisecond)
			return
		}
		_, err = service.RecoverResults(t.Context(), "a", "retained", output)
		t.Fatalf("child returned before interruption: %v", err)
	}
	url, endpoint := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if url == "" || endpoint == "" {
		t.Skip("TEST_VALKEY_URL and TEST_BLOB_S3_ENDPOINT are required")
	}
	for _, stage := range []string{"publication", "metadata"} {
		t.Run(stage, func(t *testing.T) {
			cfg := aggregateSharedConfig{URL: url, Namespace: "aggregate:" + rand.Text() + ":", Endpoint: endpoint, Bucket: "batch-recovery-" + strings.ToLower(rand.Text()), Marker: filepath.Join(t.TempDir(), "marker"), Stage: stage}
			client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("starport-test", "starport-local-test-only", "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
			_, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)})
			require.NoError(t, err)
			t.Cleanup(func() { removeAggregateTestBucket(t, client, cfg.Bucket) })
			_, err = client.PutBucketVersioning(t.Context(), &s3.PutBucketVersioningInput{Bucket: aws.String(cfg.Bucket), VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled}})
			require.NoError(t, err)
			kv, assets := openSharedAggregate(t, cfg)
			records, fs, meter, _, batch := retainedBatchOn(t, kv, assets)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				keys, err := kv.ScanWithPrefix(ctx, "", 0)
				require.NoError(t, err)
				require.NoError(t, kv.BatchDelete(ctx, keys))
			})
			commandFor := func(config aggregateSharedConfig) *exec.Cmd {
				data, err := json.Marshal(config)
				require.NoError(t, err)
				command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBatchAggregateSharedProcessRecovery$", "-test.timeout=30s")
				command.Env = append(os.Environ(), aggregateSharedChild+"="+string(data))
				return command
			}
			command := commandFor(cfg)
			var log bytes.Buffer
			command.Stdout, command.Stderr = &log, &log
			require.NoError(t, command.Start())
			t.Cleanup(func() { _ = command.Process.Kill() })
			var original []byte
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				original, _ = os.ReadFile(cfg.Marker)
				if len(original) != 0 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			require.NoError(t, command.Process.Kill())
			require.Error(t, command.Wait())
			require.NotEmpty(t, original, "child missed interruption boundary: %s", log.String())
			cfg.Stage = "recover"
			type outcome struct {
				log []byte
				err error
			}
			outcomes := make(chan outcome, 2)
			var workers sync.WaitGroup
			for range 2 {
				command := commandFor(cfg)
				workers.Go(func() { output, err := command.CombinedOutput(); outcomes <- outcome{output, err} })
			}
			workers.Wait()
			close(outcomes)
			for result := range outcomes {
				require.NoError(t, result.err, "%s", result.log)
			}
			final, err := records.Get(t.Context(), "a", batch.ID)
			require.NoError(t, err)
			require.True(t, final.RunFinished)
			require.True(t, final.ResultsReleased)
			require.Equal(t, string(original), final.OutputFileID)
			for number, id := range []string{final.OutputFileID, final.ErrorFileID} {
				_, reader, err := fs.Open(t.Context(), "a", id)
				require.NoError(t, err)
				content, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
				require.Equal(t, fmt.Sprintf("{\"line\":%d}\n", number+1), string(content))
			}
			total, err := meter.Total(t.Context(), "a")
			require.NoError(t, err)
			require.EqualValues(t, 22, total)
			visible, err := fs.List(t.Context(), "a", 100)
			require.NoError(t, err)
			require.Len(t, visible, 2)
		})
	}
}

func removeAggregateTestBucket(t *testing.T, client *s3.Client, bucket string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pages := s3.NewListObjectVersionsPaginator(client, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		require.NoError(t, err)
		var objects []s3types.ObjectIdentifier
		for _, version := range page.Versions {
			objects = append(objects, s3types.ObjectIdentifier{Key: version.Key, VersionId: version.VersionId})
		}
		for _, marker := range page.DeleteMarkers {
			objects = append(objects, s3types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
		}
		if len(objects) != 0 {
			result, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(bucket), Delete: &s3types.Delete{Objects: objects}})
			require.NoError(t, err)
			require.Empty(t, result.Errors)
		}
	}
	_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
}
