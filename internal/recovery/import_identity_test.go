package recovery

import (
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestPreparedImportIdentityMatchesEveryNativeOwner(t *testing.T) {
	source, capture, directory := backupBundleFixture(t)
	manifest, err := BackupBundle(t.Context(), directory, source, capture)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: directory, ManifestSHA256: digest}, source.Encryption)
	require.NoError(t, err)
	operation := RestoreOperation{ID: "inspect-recovered", FencingEvidence: "independent-fence"}
	identity, err := verified.ImportIdentity(operation)
	require.NoError(t, err)
	target, _, _ := bundleRestoreTargets(t, false)
	prepared, err := verified.Prepare(t.Context(), target, operation)
	require.NoError(t, err)
	require.Equal(t, prepared.Boundary, identity.Boundary)
	require.NoError(t, target.SQL.CheckRelationalImportPosition(t.Context(), manifest.SQL, identity.SQL, sqlstore.RelationalReplayPosition{}))
	require.NoError(t, target.KV.(storage.ImportInspector).InspectImport(t.Context(), identity.KVClaim, storage.ImportReplayPosition{}, func(storage.TransferRecord) error { return nil }))
	require.NoError(t, target.Blobs.(blob.ImportInspector).CheckImport(t.Context(), identity.ComponentOperation, manifest.Blobs))
	for _, changed := range []RestoreOperation{{ID: operation.ID, FencingEvidence: "other-fence"}, {ID: "other-operation", FencingEvidence: operation.FencingEvidence}} {
		other, err := verified.ImportIdentity(changed)
		require.NoError(t, err)
		require.NotEqual(t, identity.ComponentOperation, other.ComponentOperation)
		require.ErrorIs(t, target.SQL.CheckRelationalImportPosition(t.Context(), manifest.SQL, other.SQL, sqlstore.RelationalReplayPosition{}), sqlstore.ErrImportRestricted)
	}
	identity.KVClaim[0] = '!'
	again, err := verified.ImportIdentity(operation)
	require.NoError(t, err)
	require.NoError(t, target.KV.(storage.ImportInspector).InspectImport(t.Context(), again.KVClaim, storage.ImportReplayPosition{}, func(storage.TransferRecord) error { return nil }))
	var absent *RestoreSource
	_, err = absent.ImportIdentity(operation)
	require.Error(t, err)
}
