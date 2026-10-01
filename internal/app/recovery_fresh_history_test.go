package app

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// This case shares one actual backup and original history across ordered native phases.
// Each normal constructor runs in a fresh OS process against the retained target.
func TestRecoveryFreshReplicaActualHistoryBarriers(t *testing.T) {
	cfg, request := activationFleetFixture(t)
	store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	originalClaim, err := store.Get(t.Context(), storage.TransferBarrierKey)
	require.NoError(t, err)
	require.NotEmpty(t, originalClaim)
	refuse := func(t *testing.T, selected *config.Config, sqlImport bool) {
		t.Helper()
		before := censusFreshHistory(t, selected, request)
		reply := freshHistoryConstruction(t, selected)
		require.False(t, reply.Started, "a fresh replica cannot start with incomplete or invalid native recovery ownership")
		require.False(t, reply.OtherError, "refusal must identify native recovery ownership")
		if sqlImport {
			require.True(t, reply.SQLImport)
		} else {
			require.True(t, reply.KVImport || reply.Closed || reply.Conflict || reply.Incarnation)
		}
		require.True(t, reflect.DeepEqual(before, censusFreshHistory(t, selected, request)), "refused startup must preserve complete bounded native state and original files")
	}
	t.Run("prepared-original-history-not-replayed", func(t *testing.T) {
		refuse(t, cfg, true)
	})
	sealed, native := sealActivationFixture(t, cfg, request)
	t.Cleanup(func() {
		if native != nil {
			require.NoError(t, native.close())
		}
	})
	request.ExpectedDecisionSHA256 = sealed.journal.Digest()
	t.Run("sealed-history-native-owners-closed", func(t *testing.T) {
		refuse(t, cfg, true)
	})
	commitActivationNativePhases(t, cfg, sealed, native, 1)
	t.Run("blob-native-completed-sql-kv-closed", func(t *testing.T) {
		refuse(t, cfg, true)
	})
	commitActivationNativePhases(t, cfg, sealed, native, 2)
	t.Run("kv-native-completed-sql-closed", func(t *testing.T) {
		refuse(t, cfg, true)
	})
	require.NoError(t, native.close())
	native = nil
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	completed, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, completed.HistoricallyComplete)
	require.True(t, completed.CurrentAdmissionValid)
	// TestRecoveredSharedGatewayFreshStartupAndBudgetAdmission qualifies normal
	// startup and actual admission against completed imports.
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	witness, err := recovery.New(db)
	require.NoError(t, err)
	approval, err := witness.Approved(t.Context(), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	var epoch, gate, bootstrap int64
	var backend, evidence string
	require.NoError(t, db.QueryRowContext(t.Context(), db.Bind("SELECT epoch,gate_open,backend_id,evidence,bootstrap_allowed FROM catalog_recovery WHERE deployment_id=?"), approval.DeploymentID).Scan(&epoch, &gate, &backend, &evidence, &bootstrap))
	originalAuthority, err := store.Get(t.Context(), "recovery:authority:v1")
	require.NoError(t, err)
	restoreSQL := func(t *testing.T) {
		t.Helper()
		_, err := db.ExecContext(t.Context(), db.Bind("DELETE FROM catalog_recovery WHERE deployment_id=?"), approval.DeploymentID)
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), db.Bind("INSERT INTO catalog_recovery(deployment_id,epoch,gate_open,backend_id,evidence,bootstrap_allowed) VALUES(?,?,?,?,?,?)"), approval.DeploymentID, epoch, gate, backend, evidence, bootstrap)
		require.NoError(t, err)
	}
	t.Run("completed-import-missing-sql-approval", func(t *testing.T) {
		_, err := db.ExecContext(t.Context(), db.Bind("DELETE FROM catalog_recovery WHERE deployment_id=?"), approval.DeploymentID)
		require.NoError(t, err)
		defer restoreSQL(t)
		// Retained SQL activation requires its independently approved witness at open.
		refuse(t, cfg, true)
	})
	t.Run("completed-sql-original-kv-import-barrier", func(t *testing.T) {
		// Fault injection restores exact original bytes, never a fabricated claim.
		require.NoError(t, store.Set(t.Context(), storage.TransferBarrierKey, originalClaim))
		defer func() { require.NoError(t, store.Delete(t.Context(), storage.TransferBarrierKey)) }()
		refuse(t, cfg, false)
	})
	t.Run("completed-import-missing-native-approval", func(t *testing.T) {
		require.NoError(t, store.Delete(t.Context(), "recovery:authority:v1"))
		defer func() { require.NoError(t, store.Set(t.Context(), "recovery:authority:v1", originalAuthority)) }()
		refuse(t, cfg, false)
	})
	t.Run("completed-import-prior-native-epoch", func(t *testing.T) {
		closed, err := witness.Close(t.Context(), approval)
		require.NoError(t, err)
		_, err = witness.ApproveAuthority(t.Context(), store.(storage.IncarnationProvider), closed, approval.BackendID, "fixture-independent-reviewed-history", "fixture-next-epoch")
		require.NoError(t, err)
		// The original native epoch cannot satisfy a later independently approved epoch.
		require.NoError(t, store.Set(t.Context(), "recovery:authority:v1", originalAuthority))
		defer restoreSQL(t)
		refuse(t, cfg, false)
	})
	t.Run("completed-import-distinct-native-incarnation", func(t *testing.T) {
		address := os.Getenv("TEST_VALKEY_REPLACEMENT_URL")
		if address == "" {
			t.Skip("UNVERIFIED: completed-history replacement requires a distinct native Valkey")
		}
		selected := *cfg
		selected.Storage.Valkey.URL = address
		replacement, err := storage.OpenValkey(selected.RuntimeStorage().Valkey)
		require.NoError(t, err)
		defer func() {
			keys, err := replacement.ScanWithPrefix(context.Background(), "", freshHistoryMaximumRecords+1)
			require.NoError(t, err)
			require.Less(t, len(keys), freshHistoryMaximumRecords+1)
			require.NoError(t, replacement.BatchDelete(context.Background(), keys))
			require.NoError(t, replacement.Close())
		}()
		oldIdentity, err := store.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
		require.NoError(t, err)
		newIdentity, err := replacement.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
		require.NoError(t, err)
		require.NotEqual(t, oldIdentity, newIdentity)
		keys, err := replacement.ScanWithPrefix(t.Context(), "", 1)
		require.NoError(t, err)
		require.Empty(t, keys, "fixture must not overwrite an existing replacement namespace")
		copyBudgetFleetSnapshot(t, store, replacement)
		refuse(t, &selected, false)
	})
}
