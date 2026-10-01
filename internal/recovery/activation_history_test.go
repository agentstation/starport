package recovery

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func retainedActivationFixture(t *testing.T, full bool) (historyRunnerFixture, *CompletedHistory, *ClosedFinalHistory, RetainedActivationHistoryRequest) {
	t.Helper()
	f := newHistoryRunnerFixtureWithOwners(t, false, true, "", !full, full)
	completed := f.run(t)
	finalRequest := closedFinalFixtureRequest(f)
	final, err := f.witness.FinalizeImportedHistory(t.Context(), completed, finalRequest)
	require.NoError(t, err)
	request := RetainedActivationHistoryRequest{
		History: HistoryPackageRequest{Directory: f.packageDirectory, ManifestSHA256: f.accepted.state.history.digest,
			TargetSHA256: f.request.TargetSHA256, Operation: f.accepted.state.history.manifest.Operation},
		Directory: f.accepted.state.directory, DecisionSHA256: final.Report().DecisionSHA256,
		ScratchDirectory: privateKVDirectory(t), Attestation: finalRequest.Attestation,
	}
	return f, completed, final, request
}

func retainedActivationTree(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, root := range roots {
		require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[path] = historySHA256(body)
			return nil
		}))
	}
	return result
}

func TestRetainedActivationHistoryNativeCompletionAndPassivity(t *testing.T) {
	f, completed, final, request := retainedActivationFixture(t, true)
	before := retainedActivationTree(t, f.source.request.Directory, f.packageDirectory, request.Directory)
	retained, err := OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
	require.NoError(t, err)
	require.Equal(t, final.Report(), retained.Report())
	require.NoError(t, retained.Check(t.Context(), f.source, request, f.targets.Encryption))
	require.Equal(t, final.state.record.GraphRequest.CapturedAt, retained.state.record.GraphRequest.CapturedAt)
	copyOfReport := retained.Report()
	copyOfReport.Replay.Boundary.Epoch++
	copyOfReport.Graph.References.Identity.Users++
	require.Equal(t, final.Report(), retained.Report())
	for _, change := range []string{"identity", "position", "time"} {
		t.Run("forged-private-"+change, func(t *testing.T) {
			state := *retained.state
			switch change {
			case "identity":
				state.record.GraphRequest.BlobOperation += "-changed"
			case "position":
				state.record.Replay.Positions.KV.Sequence++
			case "time":
				state.record.GraphRequest.CapturedAt = state.record.GraphRequest.CapturedAt.Add(time.Second)
			}
			forged := &RetainedActivationHistory{state: &state}
			require.ErrorIs(t, forged.Check(t.Context(), f.source, request, f.targets.Encryption), ErrConflict)
		})
	}
	require.Equal(t, before, retainedActivationTree(t, f.source.request.Directory, f.packageDirectory, request.Directory))
	require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
	require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, f.targets.Blobs.CheckImportPosition(t.Context(), completed.runner.identity.ComponentOperation, completed.runner.identity.BlobOriginal, completed.Report().Positions.Blobs))
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%p"} {
		require.NotContains(t, fmt.Sprintf(verb, *retained), "private-operator")
		require.NotContains(t, fmt.Sprintf(verb, *retained), "private-acceptance-reference")
	}
}

func TestRetainedActivationHistoryImportJournalRefusesAdoptionMode(t *testing.T) {
	f, _, final, request := retainedActivationFixture(t, false)
	body, err := os.ReadFile(filepath.Join(request.Directory, "acceptance.json"))
	require.NoError(t, err)
	var retainedAcceptance historyAcceptance
	require.NoError(t, json.Unmarshal(body, &retainedAcceptance, json.RejectUnknownMembers(true)))
	require.Nil(t, retainedAcceptance.Adoption, "a zero prior approval keeps the import journal")
	before := retainedActivationTree(t, f.source.request.Directory, f.packageDirectory, request.Directory)
	retained, err := OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
	require.NoError(t, err)
	require.Equal(t, final.Report(), retained.Report())
	// This prior approval binds the captured boundary, so only the journal mode separates the two requests.
	prior := f.source.manifest.Request.Boundary
	prior.Epoch--
	prior.Open = true
	adoption := request
	adoption.PriorApproval = prior
	_, err = OpenRetainedActivationHistory(t.Context(), f.source, adoption, f.targets.Encryption)
	require.ErrorIs(t, err, ErrConflict)
	for _, binding := range []error{ErrAdoptionPrefix, ErrRestoredWitness, ErrEpochConflict} {
		require.NotErrorIs(t, err, binding, "the retained acceptance bytes refuse the adoption mode")
	}
	require.ErrorIs(t, retained.Check(t.Context(), f.source, adoption, f.targets.Encryption), ErrConflict)
	require.Equal(t, before, retainedActivationTree(t, f.source.request.Directory, f.packageDirectory, request.Directory))
}

func TestRetainedActivationHistoryAfterNativeBarrierRemovalPreservesLaterState(t *testing.T) {
	f, completed, final, request := retainedActivationFixture(t, false)
	report := completed.Report()
	importIdentity := completed.runner.identity
	require.NoError(t, f.targets.Blobs.(blob.ImportReplayActivator).ActivateImportAt(t.Context(), importIdentity.ComponentOperation, importIdentity.BlobOriginal, report.Positions.Blobs, request.DecisionSHA256))
	require.NoError(t, f.targets.KV.(storage.ImportReplayActivator).ActivateImportAt(t.Context(), importIdentity.KVClaim, report.Positions.KV, request.DecisionSHA256))
	require.NoError(t, f.witness.db.ActivateRelationalImportAt(t.Context(), importIdentity.SQLOriginal, importIdentity.SQL, report.Positions.SQL, request.DecisionSHA256, func(context.Context, *sql.Conn) error { return nil }))
	require.NoError(t, storage.CheckImportBarrier(t.Context(), f.kv))
	require.NoError(t, f.witness.db.CheckImportBarrier(t.Context()))
	people, err := identity.Open(f.witness.db)
	require.NoError(t, err)
	_, err = people.Users.Create(t.Context(), identity.User{ID: "later", Subject: "later-private-subject"})
	require.NoError(t, err)
	require.NoError(t, f.kv.Set(t.Context(), "later-domain-state", []byte("later value")))
	before := retainedActivationTree(t, f.source.request.Directory, f.packageDirectory, request.Directory)
	retained, err := OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
	require.NoError(t, err)
	require.NoError(t, retained.Check(t.Context(), f.source, request, f.targets.Encryption))
	require.Equal(t, final.Report(), retained.Report())
	require.Equal(t, before, retainedActivationTree(t, f.source.request.Directory, f.packageDirectory, request.Directory))
	value, err := f.kv.Get(t.Context(), "later-domain-state")
	require.NoError(t, err)
	require.Equal(t, []byte("later value"), value)
	later, err := people.Users.GetByID(t.Context(), "later")
	require.NoError(t, err)
	require.Equal(t, "later-private-subject", later.User.Subject)
	require.True(t, retained.Report().Restricted)
}

func TestRetainedActivationHistoryRefusesChangedScope(t *testing.T) {
	f, _, _, original := retainedActivationFixture(t, false)
	retained, err := OpenRetainedActivationHistory(t.Context(), f.source, original, f.targets.Encryption)
	require.NoError(t, err)
	for _, change := range []string{"decision", "history", "target", "operation", "operator", "reference", "fence", "coverage", "admitted-work", "directory", "scratch-overlap"} {
		t.Run(change, func(t *testing.T) {
			request := original
			switch change {
			case "decision":
				request.DecisionSHA256 = strings.Repeat("f", 64)
			case "history":
				request.History.ManifestSHA256 = strings.Repeat("f", 64)
			case "target":
				request.History.TargetSHA256 = strings.Repeat("f", 64)
			case "operation":
				request.History.Operation.ID += "-changed"
			case "operator":
				request.Attestation.Operator += "-changed"
			case "reference":
				request.Attestation.Reference += "-changed"
			case "fence":
				request.Attestation.WritersFenced = false
			case "coverage":
				request.Attestation.CompleteInterval = false
			case "admitted-work":
				request.Attestation.AdmittedWorkAccounted = false
			case "directory":
				request.Directory = privateKVDirectory(t)
			case "scratch-overlap":
				request.ScratchDirectory = original.Directory
			}
			_, err := OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
			require.Error(t, err)
			require.Error(t, retained.Check(t.Context(), f.source, request, f.targets.Encryption))
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = OpenRetainedActivationHistory(canceled, f.source, original, f.targets.Encryption)
	require.ErrorIs(t, err, context.Canceled)
	var absent *RetainedActivationHistory
	require.ErrorIs(t, absent.Check(t.Context(), f.source, original, f.targets.Encryption), ErrConflict)
	require.True(t, absent.Report().Restricted)
	_, err = OpenRetainedActivationHistory(t.Context(), f.source, original, nil)
	require.ErrorIs(t, err, ErrConflict)
}

func TestRetainedActivationHistoryRefusesMissingOrCorruptEvidenceWithoutRepair(t *testing.T) {
	f, completed, final, request := retainedActivationFixture(t, true)
	retained, err := OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
	require.NoError(t, err)
	paths := []string{
		filepath.Join(f.source.request.Directory, bundleManifestFile),
		filepath.Join(f.source.request.Directory, bundleKVFile),
		filepath.Join(f.source.request.Directory, bundleSQLFile),
		filepath.Join(f.source.request.Directory, bundleBlobFile),
		filepath.Join(f.packageDirectory, "history.json"), filepath.Join(f.packageDirectory, "payloads/000001.json"),
		filepath.Join(f.packageDirectory, "assets/000001.bin"), filepath.Join(request.Directory, "acceptance.json"),
		filepath.Join(request.Directory, "runner.json"), filepath.Join(request.Directory, historyStepName(1, "prepared")),
		filepath.Join(request.Directory, historyStepName(1, "applied")), filepath.Join(request.Directory, closedFinalHistoryFile),
		filepath.Join(request.Directory, closedFinalPreparationFile),
		filepath.Join(request.Directory, final.state.record.GraphDirectory) + ".json",
		filepath.Join(request.Directory, final.state.record.GraphDirectory, "kv/snapshot.json"),
		filepath.Join(request.Directory, final.state.record.GraphDirectory, "kv/kv.db"),
		filepath.Join(request.Directory, final.state.record.GraphDirectory, "sql/snapshot.json"),
		filepath.Join(request.Directory, final.state.record.GraphDirectory, "sql/starport.db"),
		filepath.Join(request.Directory, final.state.record.GraphDirectory, "blobs.tar"),
	}
	for _, step := range completed.runner.accepted.state.history.manifest.Steps {
		var prepared historyPreparedStep
		_, err := completed.runner.readJournalRecord(t.Context(), historyStepName(step.Ordinal, "prepared"), &prepared)
		require.NoError(t, err)
		image := completed.runner.imagePath(prepared)
		switch historyNativeOwner(step.Kind) {
		case historyOwnerKV:
			paths = append(paths, filepath.Join(image, "kv/snapshot.json"), filepath.Join(image, "kv/kv.db"), filepath.Join(image, "blobs.tar"))
		case historyOwnerSQL:
			paths = append(paths, filepath.Join(image, "sql/starport.db"))
		case historyOwnerBlob:
			paths = append(paths, filepath.Join(image, "blobs.tar"))
		}
	}
	for i, path := range paths {
		t.Run(fmt.Sprintf("%02d-%s", i, filepath.Base(path)), func(t *testing.T) {
			original, err := os.ReadFile(path)
			require.NoError(t, err)
			for _, mode := range []string{"corrupt", "missing"} {
				t.Run(mode, func(t *testing.T) {
					if mode == "corrupt" {
						require.NoError(t, os.WriteFile(path, []byte("corrupt retained evidence"), 0o600))
					} else {
						require.NoError(t, os.Remove(path))
					}
					require.Error(t, retained.Check(t.Context(), f.source, request, f.targets.Encryption))
					if mode == "corrupt" {
						actual, err := os.ReadFile(path)
						require.NoError(t, err)
						require.Equal(t, []byte("corrupt retained evidence"), actual)
					} else {
						_, err := os.Stat(path)
						require.ErrorIs(t, err, os.ErrNotExist)
					}
					require.NoError(t, os.WriteFile(path, original, 0o600))
				})
			}
		})
	}
	require.NoError(t, retained.Check(t.Context(), f.source, request, f.targets.Encryption))
}

func TestRetainedActivationHistoryRefusesUnsupportedCatalogJournal(t *testing.T) {
	f, _, _, request := retainedActivationFixture(t, false)
	path := filepath.Join(request.Directory, "catalog-prepared.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	_, err := OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
	require.ErrorIs(t, err, ErrConflict)
}

func TestRetainedActivationHistoryRefusesNoncanonicalFinalRecord(t *testing.T) {
	f, _, final, request := retainedActivationFixture(t, false)
	body, err := json.Marshal(final.state.record, json.Deterministic(true))
	require.NoError(t, err)
	body = append(body, '\n')
	require.NoError(t, os.WriteFile(filepath.Join(request.Directory, closedFinalHistoryFile), body, 0o600))
	request.DecisionSHA256 = historySHA256(body)
	_, err = OpenRetainedActivationHistory(t.Context(), f.source, request, f.targets.Encryption)
	require.ErrorIs(t, err, ErrConflict)
}
