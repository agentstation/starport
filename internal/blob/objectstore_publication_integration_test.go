package blob_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
)

// This test needs an isolated S3 service, not the in-process HTTP fixture.
// The test retains its bucket so a failed retirement remains inspectable.
func TestRealObjectStoreConditionalRetirement(t *testing.T) {
	endpoint := os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_BLOB_S3_ENDPOINT is not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	options := blob.ObjectStoreOptions{Endpoint: endpoint, Region: "us-east-1", Bucket: "retirement-" + time.Now().UTC().Format("20060102150405.000000000"), AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"}
	client := s3.NewFromConfig(aws.Config{Region: options.Region, Credentials: credentials.NewStaticCredentialsProvider(options.AccessKeyID, options.SecretAccessKey, "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(options.Bucket)})
	require.NoError(t, err)
	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(options.Bucket), VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled}})
	require.NoError(t, err)
	direct, err := blob.NewObjectStore(ctx, options)
	require.NoError(t, err)
	for _, payload := range []string{"", "small payload", strings.Repeat("x", 6*1024*1024)} {
		key := "size-" + time.Now().UTC().Format("150405.000000000")
		info, err := direct.Publish(ctx, key, strings.NewReader(payload))
		require.NoError(t, err)
		require.Equal(t, int64(len(payload)), info.Size)
		reader, err := direct.ReadPublished(ctx, key)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, payload, string(data))
		_, err = direct.Publish(ctx, key, strings.NewReader("replace"))
		require.ErrorIs(t, err, blob.ErrPublicationExists)
		require.NoError(t, direct.Retire(ctx, key))
		_, err = direct.Publish(ctx, key, strings.NewReader("late"))
		require.ErrorIs(t, err, blob.ErrPublicationExists)
		_, err = direct.StatPublished(ctx, key)
		require.ErrorIs(t, err, blob.ErrNotFound)
	}
	target, err := url.Parse(endpoint)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("uploadId") != "" {
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("multipart completion omitted conditional creation")
			}
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer func() {
		releaseOnce.Do(func() { close(release) })
		server.Close()
	}()
	options.Endpoint = server.URL
	writer, err := blob.NewObjectStore(ctx, options)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := writer.Publish(ctx, "delayed-multipart", strings.NewReader(strings.Repeat("x", 6*1024*1024)))
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("multipart writer did not reach completion")
	}
	require.NoError(t, direct.Retire(ctx, "delayed-multipart"))
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		require.ErrorIs(t, err, blob.ErrPublicationExists)
	case <-ctx.Done():
		t.Fatal("multipart writer did not finish")
	}
	_, err = direct.ReadPublished(ctx, "delayed-multipart")
	require.ErrorIs(t, err, blob.ErrNotFound)
	t.Logf("qualified versioned bucket %s", options.Bucket)
}
