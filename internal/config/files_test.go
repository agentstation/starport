package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAbsentFilesConfigSelectsTheFilesystem holds FIL-V05. The filesystem
// backend needs nothing configured, so a deployment that says nothing about
// file storage still starts and still serves an upload.
func TestAbsentFilesConfigSelectsTheFilesystem(t *testing.T) {
	t.Parallel()

	var files FilesConfig
	require.Equal(t, BlobBackendFilesystem, files.SelectedBackend())
	require.NoError(t, files.Validate())

	// A stated but empty value reads the same way. An operator who clears the
	// setting means the default rather than a broken deployment.
	files.Backend = "   "
	require.Equal(t, BlobBackendFilesystem, files.SelectedBackend())
	require.NoError(t, files.Validate())

	// The whole configuration validates with no file section at all.
	cfg := &Config{}
	require.NoError(t, cfg.Files.Validate())
	require.Equal(t, BlobBackendFilesystem, cfg.Files.SelectedBackend())
}

func TestFilesConfigRefusesAnUnknownBackend(t *testing.T) {
	t.Parallel()

	files := FilesConfig{Backend: "gcs"}
	err := files.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), BlobBackendFilesystem)
	require.Contains(t, err.Error(), BlobBackendObjectStore)
}

// TestIncompleteObjectStoreConfigRefusesStartup holds FIL-V06. An incomplete
// selection stops the gateway rather than falling back to the filesystem. A
// deployment that asked for a shared store and silently got a per-node
// directory would serve a file on one node and a not-found result on the next.
func TestIncompleteObjectStoreConfigRefusesStartup(t *testing.T) {
	t.Parallel()

	const accessKey = "AKIAEXAMPLEACCESSKEY"
	const secretKey = "wJalrXUtnFEMIEXAMPLEKEYSECRETVALUE"

	cases := map[string]FilesConfig{
		"no bucket": {
			Backend:     BlobBackendObjectStore,
			ObjectStore: ObjectStoreConfig{Region: "us-east-1", AccessKeyID: accessKey, SecretAccessKey: secretKey},
		},
		"no region and no endpoint": {
			Backend:     BlobBackendObjectStore,
			ObjectStore: ObjectStoreConfig{Bucket: "starport-files", AccessKeyID: accessKey, SecretAccessKey: secretKey},
		},
		"nothing at all": {
			Backend: BlobBackendObjectStore,
		},
		"half a credential pair": {
			Backend: BlobBackendObjectStore,
			ObjectStore: ObjectStoreConfig{
				Bucket: "starport-files", Region: "us-east-1", AccessKeyID: accessKey,
			},
		},
	}

	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := files.Validate()
			require.ErrorIs(t, err, ErrIncompleteBlobConfig)

			// The error names the missing setting and never a credential. An
			// operator pastes a startup failure into an issue, and a key
			// inside it becomes a key on the internet.
			require.NotContains(t, err.Error(), accessKey)
			require.NotContains(t, err.Error(), secretKey)
		})
	}
}

func TestCompleteObjectStoreConfigValidates(t *testing.T) {
	t.Parallel()

	cases := map[string]ObjectStoreConfig{
		"aws with a region and the ambient chain": {
			Bucket: "starport-files", Region: "us-east-1",
		},
		"another implementation with an endpoint": {
			Bucket: "starport-files", Endpoint: "https://files.example.internal",
			AccessKeyID: "AKIAEXAMPLEACCESSKEY", SecretAccessKey: "wJalrXUtnFEMIEXAMPLEKEYSECRETVALUE",
		},
	}
	for name, store := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files := FilesConfig{Backend: BlobBackendObjectStore, ObjectStore: store}
			require.NoError(t, files.Validate())
		})
	}
}

// TestObjectStoreCredentialsAreRedacted holds the other half of the same rule.
// The console and the CLI print an inspection of the configuration, and the
// two key fields must not appear in it.
func TestObjectStoreCredentialsAreRedacted(t *testing.T) {
	t.Parallel()

	const accessKey = "AKIAEXAMPLEACCESSKEY"
	const secretKey = "wJalrXUtnFEMIEXAMPLEKEYSECRETVALUE"

	cfg := &Config{Files: FilesConfig{
		Backend: BlobBackendObjectStore,
		ObjectStore: ObjectStoreConfig{
			Bucket: "starport-files", Region: "us-east-1",
			Endpoint:    "https://files.example.internal/path",
			AccessKeyID: accessKey, SecretAccessKey: secretKey,
		},
	}}

	rendered := renderInspection(t, Redacted(cfg))
	require.NotContains(t, rendered, accessKey)
	require.NotContains(t, rendered, secretKey)
	// The bucket is not a secret, and an operator needs it to tell one
	// deployment from another.
	require.Contains(t, rendered, "starport-files")
}

// renderInspection encodes a redacted tree, so a test asserts on every leaf
// without walking the shape.
func renderInspection(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
