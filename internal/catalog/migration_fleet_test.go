package catalog

import (
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestFleetMigrationRequiresSameRecoveryIdentity(t *testing.T) {
	kv, witness, db := fleetTestStores(t)
	deployment := "migration-" + rand.Text()
	closed, err := witness.Initialize(t.Context(), deployment)
	require.NoError(t, err)
	provider := kv.(storage.IncarnationProvider)
	backend, err := provider.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	approved, err := witness.Approve(t.Context(), closed, backend, "isolated-migration-test")
	require.NoError(t, err)
	// This isolated fixture explicitly starts unused. Real initialization has its own native tests.
	_, err = db.ExecContext(t.Context(), db.Bind("UPDATE catalog_recovery SET bootstrap_allowed = 1 WHERE deployment_id = ?"), deployment)
	require.NoError(t, err)
	fleet, err := NewFleetStore(t.Context(), provider, witness, deployment)
	require.NoError(t, err)
	grant, err := fleet.AcquireLease(t.Context(), "owner", time.Minute)
	require.NoError(t, err)
	publication := fleetTestPublication(t, grant, runtime.FleetHead{}, "migration")
	head, err := fleet.CommitPublication(t.Context(), publication)
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(t.Context(), head, runtime.FleetHead{}))
	root := t.TempDir()
	settings := identityTestSettings(filepath.Join(root, "source"), "", "")
	settings.DeploymentID = deployment
	migration := RuntimeMigration{OperationID: "move", SourceDirectory: settings.StateDirectory, TargetDirectory: filepath.Join(root, "target"), JournalRoot: filepath.Join(root, "journal"), SourceIdentity: "instance"}
	require.Error(t, migration.bindStore(t.Context(), kv, settings), "shared migration cannot infer recovery approval from KV")
	migration = migration.WithRecoveryWitness(db)
	require.NoError(t, migration.bindStore(t.Context(), kv, settings))
	require.NoError(t, migration.verifyStore(t.Context(), kv, settings))
	receipt, err := kv.Get(t.Context(), migration.storeReceiptKey(settings))
	require.NoError(t, err)
	require.Contains(t, string(receipt), publication.Generation.Manifest.GenerationID)
	closed, err = witness.Close(t.Context(), approved)
	require.NoError(t, err)
	require.ErrorIs(t, migration.verifyStore(t.Context(), kv, settings), recovery.ErrClosed)
	_, err = witness.Approve(t.Context(), closed, backend, "new-recovery-epoch")
	require.NoError(t, err)
	require.ErrorContains(t, migration.verifyStore(t.Context(), kv, settings), "identity differs")
	retained, err := kv.Get(t.Context(), migration.storeReceiptKey(settings))
	require.NoError(t, err)
	require.Equal(t, receipt, retained, "recovery refusal preserves the original checkpoint")
}
