package recovery

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func closedFinalFixtureRequest(f historyRunnerFixture) ClosedFinalRequest {
	return ClosedFinalRequest{TargetSHA256: f.request.TargetSHA256, Attestation: acceptedAttestation(&historyRunner{accepted: f.accepted})}
}
func TestClosedFinalHistoryNativeRetainedGraphAndRetry(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			f := newHistoryRunnerFixtureWithFinalPolicy(t, shared, true, "", true)
			completed := f.run(t)
			request := closedFinalFixtureRequest(f)
			final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
			require.NoError(t, err)
			report := final.Report()
			require.True(t, report.Restricted)
			require.True(t, report.RequiresSettings)
			require.True(t, report.RequiresCanonicalFiles)
			require.True(t, report.RequiresCatalogSelection)
			require.True(t, report.RequiresTransportTrust)
			require.True(t, report.RequiresAdministratorCredentials)
			require.NotEmpty(t, report.DecisionSHA256)
			require.True(t, report.Replay.KVRotated)
			require.True(t, report.Replay.SQLRotated)
			require.NoError(t, final.Check(t.Context(), f.witness, request))
			before, err := os.ReadFile(filepath.Join(f.accepted.state.directory, closedFinalHistoryFile))
			require.NoError(t, err)
			restarted := f.run(t)
			again, err := f.witness.FinalizeImportedHistory(t.Context(), restarted, request)
			require.NoError(t, err)
			require.Equal(t, report, again.Report())
			require.Equal(t, final.state.record.GraphRequest.CapturedAt, again.state.record.GraphRequest.CapturedAt)
			after, err := os.ReadFile(filepath.Join(f.accepted.state.directory, closedFinalHistoryFile))
			require.NoError(t, err)
			require.Equal(t, before, after)
			entries, err := os.ReadDir(f.accepted.state.directory)
			require.NoError(t, err)
			graphs := 0
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "final-graph-") {
					graphs++
				}
			}
			require.Equal(t, 1, graphs)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
			require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			require.NoError(t, f.targets.Blobs.CheckImportPosition(t.Context(), completed.runner.identity.ComponentOperation, completed.runner.identity.BlobOriginal, completed.Report().Positions.Blobs))
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%p"} {
				require.NotContains(t, fmt.Sprintf(verb, *final), "private-operator")
			}
		})
	}
}
func TestClosedFinalHistoryRefusesIncompleteAndChangedFence(t *testing.T) {
	f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
	completed := f.run(t)
	original := closedFinalFixtureRequest(f)
	for _, change := range []string{"target", "operator", "reference", "fence", "coverage", "admitted-work"} {
		t.Run(change, func(t *testing.T) {
			request := original
			switch change {
			case "target":
				request.TargetSHA256 = strings.Repeat("f", 64)
			case "operator":
				request.Attestation.Operator = "another-operator"
			case "reference":
				request.Attestation.Reference = "another-reference"
			case "fence":
				request.Attestation.WritersFenced = false
			case "coverage":
				request.Attestation.CompleteInterval = false
			case "admitted-work":
				request.Attestation.AdmittedWorkAccounted = false
			}
			_, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
			require.Error(t, err)
			_, err = os.Stat(filepath.Join(f.accepted.state.directory, closedFinalHistoryFile))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
	f.accepted.state.history.manifest.Disposition = "remain_restricted"
	_, err := f.witness.FinalizeImportedHistory(t.Context(), completed, original)
	require.Error(t, err)
}
func TestClosedFinalHistoryMissingRotationsRemainClosed(t *testing.T) {
	f := newHistoryRunnerFixture(t, false, false)
	completed := f.run(t)
	_, err := f.witness.FinalizeImportedHistory(t.Context(), completed, closedFinalFixtureRequest(f))
	require.Error(t, err)
	require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
}
func TestClosedFinalHistoryRefusesChangedDecisionImagesAndNativePositions(t *testing.T) {
	for _, change := range []string{"decision", "missing-decision", "kv-image", "sql-image", "blob-image", "position", "sql-authority", "kv-authority", "forged-time"} {
		t.Run(change, func(t *testing.T) {
			f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
			completed := f.run(t)
			request := closedFinalFixtureRequest(f)
			final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
			require.NoError(t, err)
			root := f.accepted.state.directory
			graph := filepath.Join(root, final.state.record.GraphDirectory)
			switch change {
			case "decision":
				require.NoError(t, os.WriteFile(filepath.Join(root, closedFinalHistoryFile), []byte("{}"), 0o600))
			case "missing-decision":
				require.NoError(t, os.Remove(filepath.Join(root, closedFinalHistoryFile)))
			case "kv-image":
				require.NoError(t, os.WriteFile(KVSnapshotPath(filepath.Join(graph, "kv")), []byte("changed"), 0o600))
			case "sql-image":
				require.NoError(t, os.WriteFile(filepath.Join(graph, "sql", "starport.db"), []byte("changed"), 0o600))
			case "blob-image":
				require.NoError(t, os.WriteFile(filepath.Join(graph, "blobs.tar"), []byte("changed"), 0o600))
			case "position":
				position := completed.report.Positions.KV
				_, err := f.targets.KV.ReconcileImport(t.Context(), completed.runner.identity.KVClaim, position.Sequence+1, position.ReceiptSHA256, historySHA256([]byte("successor")), []storage.CompareAndSwapMutation{{Key: "test:successor", NewValue: []byte("changed")}})
				require.NoError(t, err)
			case "sql-authority":
				_, err := f.witness.db.ExecContext(t.Context(), "UPDATE authorization_revision SET epoch='older'")
				require.NoError(t, err)
			case "kv-authority":
				require.NoError(t, f.kv.Set(t.Context(), revision.StorageKey, []byte(`{"epoch":"older","sequence":1}`)))
			case "forged-time":
				final.state.record.GraphRequest.CapturedAt = final.state.record.GraphRequest.CapturedAt.AddDate(-1, 0, 0)
				body, err := json.Marshal(final.state.record, json.Deterministic(true))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(root, closedFinalHistoryFile), body, 0o600))
			}
			require.Error(t, final.Check(t.Context(), f.witness, request))
			if change == "missing-decision" {
				again, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
				require.NoError(t, err)
				require.Equal(t, final.Report(), again.Report())
			} else {
				_, err = f.witness.FinalizeImportedHistory(t.Context(), completed, request)
				require.Error(t, err)
			}
			require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
		})
	}
}
func TestClosedFinalHistoryRejectsForgedCapabilityAndGraph(t *testing.T) {
	f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
	completed := f.run(t)
	request := closedFinalFixtureRequest(f)
	var absent *ClosedFinalHistory
	require.Error(t, absent.Check(t.Context(), f.witness, request))
	require.True(t, absent.Report().Restricted)
	forged := &CompletedHistory{report: completed.Report()}
	_, err := f.witness.FinalizeImportedHistory(t.Context(), forged, request)
	require.Error(t, err)
	final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request, func(_ context.Context, _ *KVSnapshotView, _ Record) error { return ErrConflict })
	require.Error(t, err)
	require.Nil(t, final)
	_, err = os.Stat(filepath.Join(f.accepted.state.directory, closedFinalHistoryFile))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestClosedFinalHistoryMySQLNativeTarget(t *testing.T) {
	f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, sqlstore.TypeMySQL, true)
	completed := f.run(t)
	request := closedFinalFixtureRequest(f)
	final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
	require.NoError(t, err)
	require.NoError(t, final.Check(t.Context(), f.witness, request))
	again, err := f.witness.FinalizeImportedHistory(t.Context(), f.run(t), request)
	require.NoError(t, err)
	require.Equal(t, final.Report(), again.Report())
	require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
}

func TestClosedFinalHistoryResumesCaptureAndDecisionCuts(t *testing.T) {
	for _, cut := range []string{"prepared", "partial-capture", "captured"} {
		t.Run(cut, func(t *testing.T) {
			f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
			completed := f.run(t)
			request := closedFinalFixtureRequest(f)
			prepared, err := retainClosedFinalPreparation(t.Context(), completed, request)
			require.NoError(t, err)
			if cut == "partial-capture" {
				namespace, err := completed.runner.directory.CreateChild(prepared.GraphDirectory)
				require.NoError(t, err)
				_, err = namespace.CreateChild("attempt-000001")
				require.NoError(t, err)
			} else if cut == "captured" {
				_, err = completeClosedFinalCapture(t.Context(), f.witness, completed, prepared, nil)
				require.NoError(t, err)
			}
			final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
			require.NoError(t, err)
			require.Equal(t, prepared.GraphRequest.CapturedAt, final.state.record.GraphRequest.CapturedAt)
			if cut == "partial-capture" {
				require.True(t, strings.HasSuffix(final.state.record.GraphDirectory, "attempt-000002"))
			}
			again, err := f.witness.FinalizeImportedHistory(t.Context(), f.run(t), request)
			require.NoError(t, err)
			require.Equal(t, final.Report(), again.Report())
			require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
		})
	}
}
func TestClosedFinalHistoryRefusesLostPreparationAndBoundedCaptureAttempts(t *testing.T) {
	for _, failure := range []string{"lost-preparation", "lost-partial-preparation", "corrupt-preparation", "attempt-limit"} {
		t.Run(failure, func(t *testing.T) {
			f := newHistoryRunnerFixtureWithFinalPolicy(t, false, true, "", true)
			completed := f.run(t)
			request := closedFinalFixtureRequest(f)
			if failure == "attempt-limit" || failure == "lost-partial-preparation" {
				prepared, err := retainClosedFinalPreparation(t.Context(), completed, request)
				require.NoError(t, err)
				namespace, err := completed.runner.directory.CreateChild(prepared.GraphDirectory)
				require.NoError(t, err)
				if failure == "lost-partial-preparation" {
					_, err = namespace.CreateChild("attempt-000001")
					require.NoError(t, err)
					require.NoError(t, os.Remove(filepath.Join(f.accepted.state.directory, closedFinalPreparationFile)))
				} else {
					for i := 1; i <= closedFinalMaxAttempts; i++ {
						_, err = namespace.CreateChild(fmt.Sprintf("attempt-%06d", i))
						require.NoError(t, err)
					}
				}
			} else {
				final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
				require.NoError(t, err)
				path := filepath.Join(f.accepted.state.directory, closedFinalPreparationFile)
				if failure == "lost-preparation" {
					require.NoError(t, os.Remove(path))
				} else {
					require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
				}
				require.Error(t, final.Check(t.Context(), f.witness, request))
			}
			_, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
			require.Error(t, err)
		})
	}
}

func TestClosedFinalHistoryFullOwnerCompositionAndRestart(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			f := newHistoryRunnerFixtureWithOwners(t, shared, true, "", false, true)
			completed := f.run(t)
			replay := completed.Report()
			require.Equal(t, 8, replay.CompletedSteps)
			require.EqualValues(t, 4, replay.Positions.KV.Sequence)
			require.EqualValues(t, 3, replay.Positions.SQL.Sequence)
			require.EqualValues(t, 1, replay.Positions.Blobs.Sequence)
			request := closedFinalFixtureRequest(f)
			final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, request)
			require.NoError(t, err)
			report := final.Report()
			require.Equal(t, replay, report.Replay)
			require.EqualValues(t, 1, report.Graph.References.FileRecords)
			require.EqualValues(t, 1, report.Graph.References.StoredByteClaims)
			require.EqualValues(t, 1, report.Graph.References.Identity.Users)
			require.Zero(t, report.Graph.References.Identity.Grants)
			require.True(t, report.Restricted)
			require.True(t, report.RequiresSettings && report.RequiresCanonicalFiles && report.RequiresCatalogSelection && report.RequiresTransportTrust && report.RequiresAdministratorCredentials)
			require.NoError(t, final.Check(t.Context(), f.witness, request))
			people, err := identity.Open(f.witness.db)
			require.NoError(t, err)
			grants, err := people.AccountGrants.ReachableAccounts(t.Context(), "person")
			require.NoError(t, err)
			require.Empty(t, grants)
			path := filepath.Join(f.accepted.state.directory, closedFinalHistoryFile)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			restarted, err := New(f.witness.db)
			require.NoError(t, err)
			verified, err := f.source.VerifyHistoryPackage(t.Context(), HistoryPackageRequest{Directory: f.packageDirectory, ManifestSHA256: f.accepted.state.history.digest, TargetSHA256: f.request.TargetSHA256, Operation: f.accepted.state.history.manifest.Operation})
			require.NoError(t, err)
			accepted, err := restarted.AcceptImportedHistory(t.Context(), f.source, verified, HistoryAcceptanceRequest{Directory: f.accepted.state.directory, Attestation: request.Attestation})
			require.NoError(t, err)
			replayed, err := restarted.ReplayImportedHistory(t.Context(), f.source, accepted, f.targets, f.request)
			require.NoError(t, err)
			again, err := restarted.FinalizeImportedHistory(t.Context(), replayed, request)
			require.NoError(t, err)
			require.NoError(t, again.Check(t.Context(), restarted, request))
			require.Equal(t, report, again.Report())
			require.Equal(t, final.state.record.GraphRequest.CapturedAt, again.state.record.GraphRequest.CapturedAt)
			require.Equal(t, final.state.record.GraphDirectory, again.state.record.GraphDirectory)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
			require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			require.NoError(t, f.targets.Blobs.CheckImportPosition(t.Context(), completed.runner.identity.ComponentOperation, completed.runner.identity.BlobOriginal, replay.Positions.Blobs))
		})
	}
}
