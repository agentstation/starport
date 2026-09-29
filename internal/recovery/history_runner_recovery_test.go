package recovery

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestHistoryRunnerSQLLostReplyAndCaptureCut(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[shared], func(t *testing.T) {
			f := newHistoryRunnerFixture(t, shared, true)
			runner, err := newHistoryRunner(t.Context(), f.witness, f.source, f.accepted, f.targets, f.request)
			require.NoError(t, err)
			state, err := runner.scanJournal(t.Context())
			require.NoError(t, err)
			first, err := runner.prepare(t.Context(), f.accepted.state.history.manifest.Steps[0], state.positions, state.previous)
			require.NoError(t, err)
			applied, err := runner.apply(t.Context(), *first)
			require.NoError(t, err)
			require.NoError(t, runner.publishApplied(t.Context(), applied))
			state, err = runner.scanJournal(t.Context())
			require.NoError(t, err)
			second, err := runner.prepare(t.Context(), f.accepted.state.history.manifest.Steps[1], state.positions, state.previous)
			require.NoError(t, err)
			// The original SQL preimage remains valid after commit without an applied journal reply.
			receipt, err := runner.apply(t.Context(), *second)
			require.NoError(t, err)
			result := f.run(t)
			require.Equal(t, 5, result.Report().CompletedSteps)
			var retained historyAppliedStep
			_, err = result.runner.readJournalRecord(t.Context(), historyStepName(2, "applied"), &retained)
			require.NoError(t, err)
			require.Equal(t, receipt, retained)
		})
	}
}
func TestHistoryRunnerPendingImageLossDoesNotRecaptureAdvancedStore(t *testing.T) {
	f := newHistoryRunnerFixture(t, false, true)
	f.targets.Blobs = &historyLostBlob{HistoryBlobTarget: f.targets.Blobs}
	_, err := f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.ErrorIs(t, err, errHistoryLostReply)
	path := filepath.Join(f.accepted.state.directory, historyStepName(1, "prepared"))
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var p historyPreparedStep
	require.NoError(t, json.Unmarshal(body, &p))
	require.NoError(t, os.Remove(filepath.Join(f.accepted.state.directory, "images", p.ImageDirectory, "blobs.tar")))
	_, err = f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.Error(t, err)
	again, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, again)
	_, err = os.Stat(filepath.Join(f.accepted.state.directory, historyStepName(1, "applied")))
	require.ErrorIs(t, err, os.ErrNotExist)
}
func TestHistoryRunnerNativeReceiptsRefuseResealedJournal(t *testing.T) {
	f := newHistoryRunnerFixture(t, false, true)
	completed := f.run(t)
	runner := completed.runner
	changed := runner.run
	changed.ValidatedAt = changed.ValidatedAt.Add(time.Second)
	body, err := json.Marshal(changed, json.Deterministic(true))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(f.accepted.state.directory, "runner.json"), body, 0o600))
	runSHA := historySHA256(body)
	previous := runSHA
	for _, step := range f.accepted.state.history.manifest.Steps {
		var p historyPreparedStep
		var a historyAppliedStep
		_, err = runner.readJournalRecord(t.Context(), historyStepName(step.Ordinal, "prepared"), &p)
		require.NoError(t, err)
		_, err = runner.readJournalRecord(t.Context(), historyStepName(step.Ordinal, "applied"), &a)
		require.NoError(t, err)
		p.RunSHA256 = runSHA
		p.PreviousSHA256 = previous
		body, err = json.Marshal(p, json.Deterministic(true))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(f.accepted.state.directory, historyStepName(step.Ordinal, "prepared")), body, 0o600))
		a.PreparedSHA256 = historySHA256(body)
		body, err = json.Marshal(a, json.Deterministic(true))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(f.accepted.state.directory, historyStepName(step.Ordinal, "applied")), body, 0o600))
		previous = historySHA256(body)
	}
	// The forged local chain is structurally consistent, but the native evidence binding differs.
	restarted, err := newHistoryRunner(t.Context(), f.witness, f.source, f.accepted, f.targets, f.request)
	require.NoError(t, err)
	_, err = restarted.scanJournal(t.Context())
	require.NoError(t, err)
	_, err = f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.Error(t, err)
}
func TestHistoryRunnerRefusesUnrecordedNativeAdvance(t *testing.T) {
	f := newHistoryRunnerFixture(t, false, true)
	completed := f.run(t)
	position := completed.Report().Positions.KV
	_, err := f.targets.KV.ReconcileImport(t.Context(), completed.runner.identity.KVClaim, position.Sequence+1, position.ReceiptSHA256, historySHA256([]byte("unrecorded")), []storage.CompareAndSwapMutation{{Key: "test:unexpected", NewValue: []byte("value")}})
	require.NoError(t, err)
	require.Error(t, completed.check(t.Context(), f.witness))
	_, err = f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.Error(t, err)
}
func TestHistoryRunnerMissingAssetKeepsNativeClaimClosed(t *testing.T) {
	f := newHistoryRunnerFixture(t, false, true)
	require.NoError(t, os.Remove(filepath.Join(f.packageDirectory, "assets", "000001.bin")))
	_, err := f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.Error(t, err)
	identity, err := f.source.ImportIdentity(f.accepted.state.history.manifest.Operation)
	require.NoError(t, err)
	require.NoError(t, f.targets.Blobs.CheckImportPosition(t.Context(), identity.ComponentOperation, identity.BlobOriginal, HistoryReplayPositions{}.Blobs))
}
