package blob_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

func TestRealObjectStorePublicationReadiness(t *testing.T) {
	endpoint := os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_BLOB_S3_ENDPOINT is not configured")
	}
	for _, fault := range []string{"none", "accept replacement put", "accept live multipart", "accept retired multipart"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			bucket := "readiness-" + time.Now().UTC().Format("20060102150405.000000000")
			client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("starport-test", "starport-local-test-only", "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
			_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
			require.NoError(t, err)
			target, err := url.Parse(endpoint)
			require.NoError(t, err)
			proxy := httputil.NewSingleHostReverseProxy(target)
			var requests, puts, completions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/retained-v1/") && r.URL.Query().Get("uploadId") == "" && r.Header.Get("If-None-Match") == "*" {
					if puts.Add(1) == 2 && fault == "accept replacement put" {
						w.WriteHeader(200)
						return
					}
				}
				if r.Method == http.MethodPost && r.URL.Query().Get("uploadId") != "" {
					number := completions.Add(1)
					if number == 2 && fault == "accept live multipart" || number == 3 && fault == "accept retired multipart" {
						w.Header().Set("Content-Type", "application/xml")
						_, _ = w.Write([]byte(`<CompleteMultipartUploadResult><ETag>fake</ETag></CompleteMultipartUploadResult>`))
						return
					}
				}
				proxy.ServeHTTP(w, r)
			}))
			defer server.Close()
			store, err := blob.NewObjectStore(ctx, blob.ObjectStoreOptions{Endpoint: server.URL, Region: "us-east-1", Bucket: bucket, AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"})
			require.NoError(t, err)
			require.Zero(t, requests.Load(), "construction must remain passive")
			err = store.EnsurePublicationReady(ctx)
			if fault != "none" {
				require.ErrorIs(t, err, blob.ErrPublicationUnavailable)
				require.True(t, errors.Is(err, blob.ErrConditionalPublicationUnsupported), "%v", err)
				return
			}
			require.NoError(t, err)
			before := requests.Load()
			for range 100 {
				require.NoError(t, store.EnsurePublicationReady(ctx))
			}
			require.Equal(t, before, requests.Load(), "warm readiness must make no requests")
		})
	}
}
