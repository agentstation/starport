package app

import (
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// TestInspectImportedFleetCatalogUsesCapturedBoundary keeps original catalog identity separate from target approval.
func TestInspectImportedFleetCatalogUsesCapturedBoundary(t *testing.T) {
	cfg, activation := activationFromActivatedFleet(t, true)
	before := populatedWitness(t, cfg).current
	require.Empty(t, before.BackendID, "the prepared target has no approved backend identity")
	request := recovery.InspectImportRequest{
		VerifyRequest: activation.Prepare.VerifyRequest, Operation: activation.Prepare.Operation,
		ExpectedBoundary: before, ValkeyIncarnation: activation.History.ValkeyIncarnation,
		Destination: filepath.Join(filepath.Dir(activation.Prepare.FilesDirectory), "fleet-inspection"),
	}
	result, err := InspectImportedBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	require.Equal(t, before, result.Request.Boundary)
	require.Equal(t, before, populatedWitness(t, cfg).current)
	require.NotEmpty(t, result.Inspection.RequestSHA256)
	_, err = storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
}
