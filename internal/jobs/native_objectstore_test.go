package jobs_test

import (
	"os"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
)

func TestNativeRetirementWithRealObjectStore(t *testing.T) {
	endpoint := os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_BLOB_S3_ENDPOINT is not configured")
	}
	options := blob.ObjectStoreOptions{Endpoint: endpoint, Region: "us-east-1", Bucket: "video-retirement-" + time.Now().UTC().Format("20060102150405.000000000"), AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"}
	client := s3.NewFromConfig(aws.Config{Region: options.Region, Credentials: credentials.NewStaticCredentialsProvider(options.AccessKeyID, options.SecretAccessKey, "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	_, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(options.Bucket)})
	require.NoError(t, err)
	_, err = client.PutBucketVersioning(t.Context(), &s3.PutBucketVersioningInput{Bucket: aws.String(options.Bucket), VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled}})
	require.NoError(t, err)
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		selected := options
		selected.Prefix = t.Name()
		assets, err := blob.NewObjectStore(t.Context(), selected)
		require.NoError(t, err)
		verifyNativeRetirement(t, store, assets)
	})
	t.Logf("qualified versioned bucket %s", options.Bucket)
}
