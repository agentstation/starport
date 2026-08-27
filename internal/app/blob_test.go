package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
)

// TestCompositionOpensTheConfiguredByteStore proves the selection reaches the
// running application. A deployment that configured a shared bucket and got a
// per-node directory would look healthy until a second node answered
// not-found, and no request would report the mistake.
func TestCompositionOpensTheConfiguredByteStore(t *testing.T) {
	cfg := validProductionConfig(t)
	application, err := New(cfg, withRuntimeFactories(explicitTestFactories()))
	require.NoError(t, err)
	require.NotNil(t, application.blobStore)
	require.Equal(t, config.BlobBackendFilesystem, application.blobStore.Backend())
	require.NoError(t, application.Close(context.Background()))
}

// TestObjectStoreCompositionReachesNoBucket holds the property that startup
// does not depend on a remote service. The bucket below does not exist and the
// endpoint answers nothing, and the gateway still boots.
func TestObjectStoreCompositionReachesNoBucket(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Files = config.FilesConfig{
		Backend: config.BlobBackendObjectStore,
		ObjectStore: config.ObjectStoreConfig{
			Bucket:          "starport-files",
			Region:          "us-east-1",
			Endpoint:        "http://127.0.0.1:1",
			AccessKeyID:     "test-access-key",
			SecretAccessKey: "test-secret-key",
		},
	}

	application, err := New(cfg, withRuntimeFactories(explicitTestFactories()))
	require.NoError(t, err)
	require.Equal(t, config.BlobBackendObjectStore, application.blobStore.Backend())
	require.NoError(t, application.Close(context.Background()))
}
