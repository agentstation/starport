package recovery

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type lostExpiringCatalogReply struct {
	HistoryKVTarget
	lost bool
}

func (l *lostExpiringCatalogReply) ReconcileExpiringImport(ctx context.Context, claim []byte, sequence int64, previous, evidence string, records []storage.TransferRecord) (string, error) {
	receipt, err := l.HistoryKVTarget.(storage.ImportExpiringRetirer).ReconcileExpiringImport(ctx, claim, sequence, previous, evidence, records)
	if err == nil && !l.lost {
		l.lost = true
		return "", errHistoryLostReply
	}
	return receipt, err
}

func TestCatalogExpiringPreparationRetainsOriginalNativeReceiptUnderSQLGuard(t *testing.T) {
	for _, backend := range []struct {
		name    string
		shared  bool
		sqlType string
	}{{"badger-sqlite", false, sqlstore.TypeSQLite}, {"valkey-postgres", true, sqlstore.TypePostgres}, {"valkey-mysql", true, sqlstore.TypeMySQL}} {
		t.Run(backend.name, func(t *testing.T) {
			f := newCatalogLaneFixture(t, backend.shared, backend.sqlType)
			lost := &lostExpiringCatalogReply{HistoryKVTarget: f.history.targets.KV}
			f.history.targets.KV = lost
			f.lane.runner.targets.KV = lost
			original := []storage.TransferRecord{{Key: "catalog:fleet:{captured}:v1:lease", Value: []byte("original captured lease"), ExpiresAtMillis: 1}}
			// The native import contract omits this already expired original control. The owning clock proves absence.
			require.ErrorIs(t, f.lane.ApplyExpiringCatalogTopology(t.Context(), f.topology, 0, original), errHistoryLostReply)
			path := filepath.Join(f.history.accepted.state.directory, "catalog-assets", catalogAssetName(0))
			retained, err := os.ReadFile(path)
			require.NoError(t, err)
			prefix, err := f.history.witness.ReplayImportedHistoryPrefix(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.NoError(t, err)
			lane, err := prefix.OpenCatalogPreparation(t.Context())
			require.NoError(t, err)
			done, err := lane.CompletedCatalogTopology(t.Context(), f.topology, 0)
			require.NoError(t, err)
			require.True(t, done)
			changed := []storage.TransferRecord{original[0]}
			changed[0].ExpiresAtMillis++
			require.Error(t, lane.ApplyExpiringCatalogTopology(t.Context(), f.topology, 0, changed))
			require.NoError(t, lane.ApplyExpiringCatalogTopology(t.Context(), f.topology, 0, original))
			repeated, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, retained, repeated)
			require.Error(t, lane.ApplyCatalogTopology(t.Context(), f.topology, 0, []storage.CompareAndSwapMutation{{Key: original[0].Key, ExpectedValue: original[0].Value}}), "a persistent stage cannot replace original expiry evidence")
			_, err = f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.Error(t, err, "selection and final authorization rotations remain required")
			require.NoError(t, lane.ApplyCatalogTopology(t.Context(), f.topology, 1, []storage.CompareAndSwapMutation{{Key: "catalog:test:selection", NewValue: []byte("selected")}}))
			complete, err := f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.NoError(t, err)
			require.True(t, complete.Report().KVRotated)
			require.True(t, complete.Report().SQLRotated)
			require.EqualValues(t, 3, complete.Report().Positions.KV.Sequence)
		})
	}
}

func TestCatalogExpiringPreparationRefusesUnownedOrMixedAssets(t *testing.T) {
	f := newCatalogLaneFixture(t, false, sqlstore.TypeSQLite)
	for _, record := range []storage.TransferRecord{{Key: "catalog:unrelated", Value: []byte("value"), ExpiresAtMillis: 1}, {Key: "catalog:fleet:{captured}:v1:head", Value: []byte("value"), ExpiresAtMillis: 1}, {Key: "catalog:fleet:{captured}:v1:lease", Value: []byte("value")}} {
		require.Error(t, f.lane.ApplyExpiringCatalogTopology(t.Context(), f.topology, 0, []storage.TransferRecord{record}))
	}
	done, err := f.lane.CompletedCatalogTopology(t.Context(), f.topology, 0)
	require.NoError(t, err)
	require.False(t, done)
	mixed := catalogStageAsset{Kind: "expiring-retirement", Expiring: []catalogExpiringAsset{{Key: []byte("catalog:fleet:{captured}:v1:lease"), Value: []byte("value"), ValuePresent: true, ExpiresAtMillis: 1}}, Mutations: []catalogMutationAsset{{Key: []byte("catalog:test"), NewPresent: true}}}
	_, err = mixed.expiringRecords()
	require.Error(t, err)
	_, err = mixed.mutations()
	require.Error(t, err)
}

func TestCatalogExpiringAssetPreservesEmptyValuePresence(t *testing.T) {
	f := newCatalogLaneFixture(t, false, sqlstore.TypeSQLite)
	original := []storage.TransferRecord{{Key: "catalog:fleet:{captured}:v1:lease", Value: []byte{}, ExpiresAtMillis: 1}}
	_, body, err := newExpiringCatalogAsset(f.lane.runner, 0, f.lane.state.positions, f.lane.state.previous, original)
	require.NoError(t, err)
	var decoded catalogStageAsset
	require.NoError(t, json.Unmarshal(body, &decoded))
	records, err := decoded.expiringRecords()
	require.NoError(t, err)
	require.NotNil(t, records[0].Value)
	decoded.Expiring[0].ValuePresent = false
	records, err = decoded.expiringRecords()
	require.NoError(t, err)
	require.Nil(t, records[0].Value)
}
