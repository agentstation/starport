package recovery

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestRestoreFileTreeValidatesInferencePolicyOwner(t *testing.T) {
	source, capture, backup := backupBundleFixture(t)
	original := filepath.Join(privateKVDirectory(t), "policy")
	owner := credentials.SelectionPolicyOwner{Product: "starport", Deployment: capture.Boundary.DeploymentID, Instance: "replica-one"}
	policy, err := credentials.OpenSelectionPolicyStore(t.Context(), original, owner, true)
	require.NoError(t, err)
	require.NoError(t, policy.Accept(t.Context(), "openai"))
	request := FileTreeRequest{Destination: filepath.Join(privateKVDirectory(t), "restored-policy")}
	require.NoError(t, filepath.WalkDir(original, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(original, name)
		if err != nil {
			return err
		}
		id := fmt.Sprintf("policy/%d", len(request.Files))
		source.Files = append(source.Files, BundleFile{ID: id, Path: name})
		request.Files = append(request.Files, FileTreeFile{ArtifactID: id, Relative: filepath.ToSlash(relative)})
		return nil
	}))
	manifest, err := BackupBundle(t.Context(), backup, source, capture)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: backup, ManifestSHA256: digest}, source.Encryption)
	require.NoError(t, err)
	other := owner
	other.Instance = "replica-two"
	result, err := verified.PublishFileTree(t.Context(), request, func(ctx context.Context, path string) error {
		return credentials.InspectSelectionPolicyDirectory(ctx, path, other)
	})
	require.Error(t, err)
	require.False(t, result.Published)
	require.NoDirExists(t, request.Destination)
	validate := func(ctx context.Context, path string) error {
		return credentials.InspectSelectionPolicyDirectory(ctx, path, owner)
	}
	result, err = verified.PublishFileTree(t.Context(), request, validate)
	require.NoError(t, err)
	require.True(t, result.Published)
	again, err := verified.PublishFileTree(t.Context(), request, validate)
	require.NoError(t, err)
	require.True(t, again.Reused)
	require.Equal(t, result.DirectoryIdentity, again.DirectoryIdentity)
	restored, err := credentials.OpenSelectionPolicyStore(t.Context(), request.Destination, owner, false)
	require.NoError(t, err)
	accepted, err := restored.Policy(t.Context(), "openai")
	require.NoError(t, err)
	require.Equal(t, credentials.InferencePolicyCurrent, accepted)
	unchanged, err := restored.Policy(t.Context(), "anthropic")
	require.NoError(t, err)
	require.Equal(t, credentials.InferencePolicyLegacy, unchanged)
}
