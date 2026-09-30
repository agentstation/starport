package recovery

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRetainedActivationRunnerPreservesDeclaredCatalogAfterRelease(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	completed := f.finish(t)
	decision := strings.Repeat("d", 64)
	identity := completed.runner.identity
	positions := completed.Report().Positions
	require.NoError(t, f.history.targets.KV.(storage.ImportReplayActivator).ActivateImportAt(t.Context(), identity.KVClaim, positions.KV, decision))
	require.NoError(t, f.history.targets.Blobs.(blob.ImportReplayActivator).ActivateImportAt(t.Context(), identity.ComponentOperation, identity.BlobOriginal, positions.Blobs, decision))
	require.NoError(t, f.history.witness.db.ActivateRelationalImportAt(t.Context(), identity.SQLOriginal, identity.SQL, positions.SQL, decision, func(context.Context, *sql.Conn) error { return nil }))
	request := RetainedActivationHistoryRequest{
		History: HistoryPackageRequest{Directory: f.history.packageDirectory, ManifestSHA256: f.history.accepted.state.history.digest,
			TargetSHA256: f.history.request.TargetSHA256, Operation: f.history.accepted.state.history.manifest.Operation},
		Directory: f.history.accepted.state.directory, ScratchDirectory: privateKVDirectory(t), DecisionSHA256: decision,
		Attestation: acceptedAttestation(completed.runner),
	}
	before := retainedActivationTree(t, request.Directory)
	runner, journal, err := openRetainedActivationRunner(t.Context(), f.history.source, request, f.history.targets.Encryption)
	require.NoError(t, err)
	require.Equal(t, completed.runner.run.ValidatedAt, runner.run.ValidatedAt)
	require.Equal(t, completed.runner.runBytes, runner.runBytes)
	require.Equal(t, positions, journal.positions)
	require.NotNil(t, journal.catalog)
	require.NotEmpty(t, journal.catalog.complete)
	require.Equal(t, before, retainedActivationTree(t, request.Directory))
	require.NoError(t, storage.CheckImportBarrier(t.Context(), f.history.kv))
	require.NoError(t, f.history.witness.db.CheckImportBarrier(t.Context()))
	asset := filepath.Join(request.Directory, "catalog-assets", catalogAssetName(0))
	body, err := os.ReadFile(asset)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(asset, append(body, '\n'), 0o600))
	_, _, err = openRetainedActivationRunner(t.Context(), f.history.source, request, f.history.targets.Encryption)
	require.Error(t, err)
}
