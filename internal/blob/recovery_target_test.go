package blob

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

func TestRecoveryTargetBindsBlobScopeWithoutCredentials(t *testing.T) {
	makeTarget := func(secret, endpoint, bucket, prefix string) objectRestoreTarget {
		client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(endpoint), Credentials: credentials.NewStaticCredentialsProvider("access", secret, "")})
		return objectRestoreTarget{store: &ObjectStore{client: client, bucket: bucket, prefix: prefix}}
	}
	before, err := makeTarget("one", "https://objects.example", "bucket", "one").RecoveryTargetSHA256()
	require.NoError(t, err)
	after, err := makeTarget("two", "https://objects.example", "bucket", "one").RecoveryTargetSHA256()
	require.NoError(t, err)
	require.Equal(t, before, after)
	for _, target := range []objectRestoreTarget{
		makeTarget("one", "https://elsewhere.example", "bucket", "one"),
		makeTarget("one", "https://objects.example", "other", "one"),
		makeTarget("one", "https://objects.example", "bucket", "two"),
	} {
		after, err = target.RecoveryTargetSHA256()
		require.NoError(t, err)
		require.NotEqual(t, before, after)
	}
	for _, endpoint := range []string{"https://user:secret@objects.example", "https://objects.example?secret=value", "ftp://objects.example"} {
		_, err = makeTarget("one", endpoint, "bucket", "one").RecoveryTargetSHA256()
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestRecoveryTargetDetectsBlobDirectoryReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.Mkdir(path, 0o700))
	target := filesystemRestoreTarget{destination: path}
	before, err := target.RecoveryTargetSHA256()
	require.NoError(t, err)
	require.NoError(t, os.Rename(path, path+"-original"))
	require.NoError(t, os.Mkdir(path, 0o700))
	after, err := target.RecoveryTargetSHA256()
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}
