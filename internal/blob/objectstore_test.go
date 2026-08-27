package blob_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
)

func openObjectStore(t *testing.T, prefix string) (*objectServer, blob.Store) {
	t.Helper()
	server, endpoint := newObjectServer(t, "starport-files")
	store, err := blob.NewObjectStore(context.Background(), blob.ObjectStoreOptions{
		Bucket:          "starport-files",
		Region:          "us-east-1",
		Endpoint:        endpoint,
		Prefix:          prefix,
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	require.NoError(t, err)
	return server, store
}

// TestObjectStoreScopesKeysUnderThePrefix asserts on the medium rather than on
// the answer. Two deployments that shared a bucket without a prefix would read
// each other's files, and every operation would still report success.
func TestObjectStoreScopesKeysUnderThePrefix(t *testing.T) {
	t.Parallel()

	server, store := openObjectStore(t, "deployment-one/")
	_, err := store.Put(context.Background(), "file-id", strings.NewReader("payload"))
	require.NoError(t, err)

	value, ok := server.stored("deployment-one/file-id")
	require.True(t, ok, "the object does not sit under the prefix")
	require.Equal(t, "payload", string(value))

	_, ok = server.stored("file-id")
	require.False(t, ok, "the object also sits outside the prefix")
}

func TestObjectStoreWithoutAPrefixUsesTheKeyItself(t *testing.T) {
	t.Parallel()

	server, store := openObjectStore(t, "")
	_, err := store.Put(context.Background(), "file-id", strings.NewReader("payload"))
	require.NoError(t, err)

	_, ok := server.stored("file-id")
	require.True(t, ok)
}

// TestObjectStoreInterruptedPutWritesNothing checks the medium for the
// property the contract states. The contract case proves the read answers
// not-found. This proves the bucket holds nothing at all, so a later sweep has
// no orphan to find.
func TestObjectStoreInterruptedPutWritesNothing(t *testing.T) {
	t.Parallel()

	server, store := openObjectStore(t, "deployment-one")
	_, err := store.Put(context.Background(), "interrupted",
		&errAfter{remaining: 4096, err: errors.New("the client went away")})
	require.Error(t, err)
	require.Equal(t, 0, server.count(), "the bucket holds an object after a failed put")
}

// TestObjectStoreRefusesAnEmptyBucket holds the one check the constructor makes
// before it builds a client. Every other reachability question waits for the
// first request, because a network call at startup would make the gateway
// depend on a remote service to boot.
func TestObjectStoreRefusesAnEmptyBucket(t *testing.T) {
	t.Parallel()

	_, err := blob.NewObjectStore(context.Background(), blob.ObjectStoreOptions{Region: "us-east-1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bucket")
}

// TestObjectStoreErrorsNameNoCredential holds the rule that the object store
// credentials stay out of every error. An operator pastes a startup failure
// into an issue, and a key inside it becomes a key on the internet.
func TestObjectStoreErrorsNameNoCredential(t *testing.T) {
	t.Parallel()

	const accessKey = "AKIAEXAMPLEACCESSKEY"
	const secretKey = "wJalrXUtnFEMIEXAMPLEKEYSECRETVALUE"

	server, endpoint := newObjectServer(t, "starport-files")
	store, err := blob.NewObjectStore(context.Background(), blob.ObjectStoreOptions{
		Bucket:          "another-bucket",
		Region:          "us-east-1",
		Endpoint:        endpoint,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
	})
	require.NoError(t, err)

	// Every operation runs against a bucket the server does not serve, so each
	// one produces a real failure to read.
	ctx := context.Background()
	_, putErr := store.Put(ctx, "file-id", strings.NewReader("payload"))
	_, getErr := store.Get(ctx, "file-id")
	_, statErr := store.Stat(ctx, "file-id")

	for name, failure := range map[string]error{"put": putErr, "get": getErr, "stat": statErr} {
		require.Errorf(t, failure, "%s reported no error", name)
		require.NotContainsf(t, failure.Error(), accessKey, "%s names the access key", name)
		require.NotContainsf(t, failure.Error(), secretKey, "%s names the secret key", name)
	}
	require.Equal(t, 0, server.count())
}

func TestObjectStoreNamesItsBackend(t *testing.T) {
	t.Parallel()

	_, store := openObjectStore(t, "deployment-one")
	require.Equal(t, "objectstore", store.Backend())
}
