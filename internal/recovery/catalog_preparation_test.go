package recovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"

	"github.com/stretchr/testify/require"
)

func TestCatalogPreparationDeclaredLaneCannotBypassFinalRotations(t *testing.T) {
	f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
	f.request.CatalogPreparation = &CatalogPreparationPlan{TopologySHA256: strings.Repeat("a", 64), StageCount: 1}
	_, err := f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.Error(t, err, "declared catalog preparation must complete before final rotations")
}

type catalogLaneFixture struct {
	history  historyRunnerFixture
	prefix   *HistoryPrefix
	lane     *CatalogPreparationLane
	topology string
}

func newCatalogLaneFixture(t *testing.T, shared bool, sqlType string) catalogLaneFixture {
	t.Helper()
	f := newHistoryRunnerFixtureWithSQL(t, shared, true, sqlType)
	topology := strings.Repeat("a", 64)
	f.request.CatalogPreparation = &CatalogPreparationPlan{TopologySHA256: topology, StageCount: 1}
	prefix, err := f.witness.ReplayImportedHistoryPrefix(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.NoError(t, err)
	lane, err := prefix.OpenCatalogPreparation(t.Context())
	require.NoError(t, err)
	return catalogLaneFixture{history: f, prefix: prefix, lane: lane, topology: topology}
}
func catalogFixtureStage() []storage.CompareAndSwapMutation {
	return []storage.CompareAndSwapMutation{{Key: "catalog:test:selection", NewValue: []byte{}}}
}
func catalogFixtureSelection() []storage.CompareAndSwapMutation {
	return []storage.CompareAndSwapMutation{{Key: "catalog:test:selection", ExpectedValue: []byte{}, NewValue: []byte("selected")}}
}
func (f catalogLaneFixture) finish(t *testing.T) *CompletedHistory {
	t.Helper()
	require.NoError(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()))
	require.NoError(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 1, catalogFixtureSelection()))
	complete, err := f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
	require.NoError(t, err)
	return complete
}
func TestCatalogPreparationNativeOwnersBridgeFinalHistory(t *testing.T) {
	for _, backend := range []struct {
		name    string
		shared  bool
		sqlType string
	}{{"badger-sqlite", false, sqlstore.TypeSQLite}, {"valkey-postgres", true, sqlstore.TypePostgres}, {"valkey-mysql", true, sqlstore.TypeMySQL}} {
		t.Run(backend.name, func(t *testing.T) {
			f := newCatalogLaneFixture(t, backend.shared, backend.sqlType)
			state, err := f.prefix.runner.scanJournal(t.Context())
			require.NoError(t, err)
			require.Equal(t, 3, state.count)
			require.EqualValues(t, 2, state.positions.SQL.Sequence)
			require.False(t, state.kvRotated)
			require.False(t, state.sqlRotated)
			require.NoError(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()))
			empty, err := f.lane.ReadCatalogTopology(t.Context(), "catalog:test:selection", 16)
			require.NoError(t, err)
			require.NotNil(t, empty)
			require.Empty(t, empty)
			_, err = f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.Error(t, err)
			require.NoError(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 1, catalogFixtureSelection()))
			done, err := f.lane.CompletedCatalogTopology(t.Context(), f.topology, 0)
			require.NoError(t, err)
			require.True(t, done)
			complete, err := f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.NoError(t, err)
			report := complete.Report()
			require.EqualValues(t, 3, report.Positions.KV.Sequence)
			require.EqualValues(t, 3, report.Positions.SQL.Sequence)
			require.True(t, report.KVRotated)
			require.True(t, report.SQLRotated)
			var prepared historyPreparedStep
			body, err := complete.runner.readJournalRecord(t.Context(), historyStepName(4, "prepared"), &prepared)
			require.NoError(t, err)
			require.NotEmpty(t, body)
			var seal catalogCompleteRecord
			sealBody, err := complete.runner.readJournalRecord(t.Context(), "catalog.complete.json", &seal)
			require.NoError(t, err)
			require.Equal(t, historySHA256(sealBody), prepared.PreviousSHA256)
			require.EqualValues(t, 2, prepared.Before.KV.Sequence)
			again, err := f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.NoError(t, err)
			require.Equal(t, report, again.Report())
			people, err := identity.Open(f.history.witness.db)
			require.NoError(t, err)
			grants, err := people.AccountGrants.ReachableAccounts(t.Context(), "person")
			require.NoError(t, err)
			require.Empty(t, grants)
		})
	}
}
func TestCatalogPreparationLostRepliesRecoverOriginalAssetsAndSelection(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			f := newCatalogLaneFixture(t, shared, "")
			lost := &historyLostKV{HistoryKVTarget: f.history.targets.KV}
			f.history.targets.KV = lost
			f.lane.runner.targets.KV = lost
			require.ErrorIs(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()), errHistoryLostReply)
			original, err := os.ReadFile(filepath.Join(f.history.accepted.state.directory, "catalog-assets", catalogAssetName(0)))
			require.NoError(t, err)
			prefix, err := f.history.witness.ReplayImportedHistoryPrefix(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.NoError(t, err)
			lane, err := prefix.OpenCatalogPreparation(t.Context())
			require.NoError(t, err)
			done, err := lane.CompletedCatalogTopology(t.Context(), f.topology, 0)
			require.NoError(t, err)
			require.True(t, done)
			require.NoError(t, lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()))
			retained, err := os.ReadFile(filepath.Join(f.history.accepted.state.directory, "catalog-assets", catalogAssetName(0)))
			require.NoError(t, err)
			require.Equal(t, original, retained)
			lost.lost = false
			require.ErrorIs(t, lane.ApplyCatalogTopology(t.Context(), f.topology, 1, catalogFixtureSelection()), errHistoryLostReply)
			prefix, err = f.history.witness.ReplayImportedHistoryPrefix(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.NoError(t, err)
			lane, err = prefix.OpenCatalogPreparation(t.Context())
			require.NoError(t, err)
			done, err = lane.CompletedCatalogTopology(t.Context(), f.topology, 1)
			require.NoError(t, err)
			require.True(t, done)
			complete, err := f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
			require.NoError(t, err)
			require.EqualValues(t, 3, complete.Report().Positions.KV.Sequence)
		})
	}
}
func TestCatalogPreparationRetainedAssetsBeforePointerRecoverExactly(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	_, asset, err := newCatalogAsset(f.lane.runner, 0, f.lane.state.positions, f.lane.state.previous, catalogFixtureStage())
	require.NoError(t, err)
	require.NoError(t, f.lane.runner.publishCatalogAsset(t.Context(), 0, asset))
	prefix, err := f.history.witness.ReplayImportedHistoryPrefix(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
	require.NoError(t, err)
	lane, err := prefix.OpenCatalogPreparation(t.Context())
	require.NoError(t, err)
	require.Error(t, lane.ApplyCatalogTopology(t.Context(), f.topology, 0, []storage.CompareAndSwapMutation{{Key: "catalog:test:selection", NewValue: []byte("different")}}))
	require.NoError(t, lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()))
	retained, err := os.ReadFile(filepath.Join(f.history.accepted.state.directory, "catalog-assets", catalogAssetName(0)))
	require.NoError(t, err)
	require.Equal(t, asset, retained)
}
func TestCatalogPreparationRefusesChangedDeclarationsAndOrder(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	require.Error(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 1, catalogFixtureSelection()))
	require.Error(t, f.lane.ApplyCatalogTopology(t.Context(), strings.Repeat("b", 64), 0, catalogFixtureStage()))
	changed := f.history.request
	different := *changed.CatalogPreparation
	different.StageCount++
	changed.CatalogPreparation = &different
	_, err := f.history.witness.ReplayImportedHistoryPrefix(t.Context(), f.history.source, f.history.accepted, f.history.targets, changed)
	require.Error(t, err)
	changed.CatalogPreparation = nil
	_, err = f.history.witness.ReplayImportedHistory(t.Context(), f.history.source, f.history.accepted, f.history.targets, changed)
	require.Error(t, err)
	require.Error(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, []storage.CompareAndSwapMutation{{Key: "account:forbidden", NewValue: []byte("value")}}))
	require.Error(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, []storage.CompareAndSwapMutation{{Key: "catalog:test:large", NewValue: make([]byte, storage.ImportReplayMaxBytes)}}))
	var tooMany []storage.CompareAndSwapMutation
	for index := 0; index < 129; index++ {
		tooMany = append(tooMany, storage.CompareAndSwapMutation{Key: fmt.Sprint("catalog:test:", index), NewValue: []byte("x")})
	}
	require.Error(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, tooMany))
}
func TestCatalogPreparationStaleNativeCursorCannotRecapture(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	_, err := f.history.targets.KV.ReconcileImport(t.Context(), f.lane.runner.identity.KVClaim, 1, "", strings.Repeat("f", 64), []storage.CompareAndSwapMutation{{Key: "catalog:test:unrecorded", NewValue: []byte("retained")}})
	require.NoError(t, err)
	require.Error(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()))
	_, err = os.Stat(filepath.Join(f.history.accepted.state.directory, "catalog-assets", catalogAssetName(0)))
	require.ErrorIs(t, err, os.ErrNotExist)
	value, err := f.history.kv.Get(t.Context(), "catalog:test:unrecorded")
	require.NoError(t, err)
	require.Equal(t, "retained", string(value))
}
func TestCatalogPreparationPassiveJournalAfterNativeReleaseRetainsLaterWithdrawal(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	complete := f.finish(t)
	r := *complete.runner
	originalTime := r.run.ValidatedAt
	decision := strings.Repeat("d", 64)
	require.NoError(t, f.history.targets.KV.(storage.ImportReplayActivator).ActivateImportAt(t.Context(), r.identity.KVClaim, complete.Report().Positions.KV, decision))
	require.NoError(t, f.history.kv.Set(t.Context(), "catalog:test:selection", []byte("withdrawn")))
	r.witness = nil
	r.targets = HistoryReplayTargets{}
	state, err := r.scanJournal(t.Context())
	require.NoError(t, err)
	require.Equal(t, complete.Report(), r.report(state))
	require.Equal(t, originalTime, r.run.ValidatedAt)
	_, err = f.lane.CompletedCatalogTopology(t.Context(), f.topology, 0)
	require.Error(t, err)
	require.Error(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()))
	value, err := f.history.kv.Get(t.Context(), "catalog:test:selection")
	require.NoError(t, err)
	require.Equal(t, "withdrawn", string(value))
}
func TestCatalogPreparationRefusesMissingCorruptAndUnexpectedJournal(t *testing.T) {
	for _, change := range []string{"asset", "prepared", "applied", "complete", "future", "extra-asset", "removed-asset"} {
		t.Run(change, func(t *testing.T) {
			f := newCatalogLaneFixture(t, false, "")
			complete := f.finish(t)
			base := f.history.accepted.state.directory
			path := ""
			switch change {
			case "asset":
				path = filepath.Join(base, "catalog-assets", catalogAssetName(0))
			case "prepared", "applied":
				path = filepath.Join(base, catalogStageName(0, change))
			case "complete":
				path = filepath.Join(base, "catalog.complete.json")
			case "future":
				path = filepath.Join(base, catalogStageName(2, "prepared"))
			case "extra-asset":
				path = filepath.Join(base, "catalog-assets", catalogAssetName(2))
			case "removed-asset":
				require.NoError(t, os.Remove(filepath.Join(base, "catalog-assets", catalogAssetName(0))))
			}
			if path != "" {
				require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
			}
			_, err := complete.runner.scanJournal(t.Context())
			require.Error(t, err)
			require.Error(t, complete.check(t.Context(), f.history.witness))
		})
	}
}

func TestCatalogPreparationRequiresExactRetainedPrefix(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	require.NoError(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()))
	require.NoError(t, os.Remove(filepath.Join(f.history.accepted.state.directory, historyStepName(3, historyAppliedPhase))))
	_, err := f.history.witness.ReplayImportedHistoryPrefix(t.Context(), f.history.source, f.history.accepted, f.history.targets, f.history.request)
	require.Error(t, err)
	_, err = f.prefix.OpenCatalogPreparation(t.Context())
	require.Error(t, err)
	require.Error(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 1, catalogFixtureSelection()))
}
func TestCatalogPreparationPendingReplyRefusesChangedOriginalPointer(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	lost := &historyLostKV{HistoryKVTarget: f.history.targets.KV}
	f.lane.runner.targets.KV = lost
	require.ErrorIs(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage()), errHistoryLostReply)
	require.NoError(t, os.WriteFile(filepath.Join(f.history.accepted.state.directory, catalogStageName(0, historyPreparedPhase)), []byte("{}"), 0600))
	_, err := f.lane.CompletedCatalogTopology(t.Context(), f.topology, 0)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(f.history.accepted.state.directory, catalogStageName(0, historyAppliedPhase)))
	require.ErrorIs(t, err, os.ErrNotExist)
}
func TestCatalogPreparationConcurrentExactStageDoesNotAdvanceTwice(t *testing.T) {
	f := newCatalogLaneFixture(t, false, "")
	results := make(chan error, 2)
	var started sync.WaitGroup
	started.Add(2)
	for range 2 {
		go func() {
			started.Done()
			started.Wait()
			results <- f.lane.ApplyCatalogTopology(t.Context(), f.topology, 0, catalogFixtureStage())
		}()
	}
	for range 2 {
		require.NoError(t, <-results)
	}
	require.EqualValues(t, 1, f.lane.state.positions.KV.Sequence)
	require.Equal(t, 1, f.lane.state.count)
	require.NoError(t, f.lane.ApplyCatalogTopology(t.Context(), f.topology, 1, catalogFixtureSelection()))
	require.EqualValues(t, 2, f.lane.state.positions.KV.Sequence)
}

func TestCatalogPreparationSelectionOnlyPrecedesBothRotations(t *testing.T) {
	f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
	topology := strings.Repeat("a", 64)
	f.request.CatalogPreparation = &CatalogPreparationPlan{TopologySHA256: topology, StageCount: 0}
	prefix, err := f.witness.ReplayImportedHistoryPrefix(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.NoError(t, err)
	lane, err := prefix.OpenCatalogPreparation(t.Context())
	require.NoError(t, err)
	require.NoError(t, lane.ApplyCatalogTopology(t.Context(), topology, 0, []storage.CompareAndSwapMutation{{Key: "catalog:test:selection", NewValue: []byte("selected")}}))
	completed, err := f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.NoError(t, err)
	require.EqualValues(t, 2, completed.Report().Positions.KV.Sequence)
	require.EqualValues(t, 1, completed.Report().Positions.SQL.Sequence)
	require.True(t, completed.Report().KVRotated)
	require.True(t, completed.Report().SQLRotated)
}
