package recovery

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type historyRunnerFixture struct {
	witness          *Witness
	source           *RestoreSource
	accepted         *AcceptedHistory
	targets          HistoryReplayTargets
	request          HistoryReplayRequest
	kv               storage.KVStore
	packageDirectory string
}

func newHistoryRunnerFixture(t *testing.T, shared, steps bool) historyRunnerFixture {
	t.Helper()
	bundle, capture, directory := backupBundleRecipe(t, shared)
	people, err := identity.Open(bundle.SQL)
	require.NoError(t, err)
	_, err = people.Users.Create(t.Context(), identity.User{ID: "person", Subject: "private-subject"})
	require.NoError(t, err)
	manifest, err := BackupBundle(t.Context(), directory, bundle, capture)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	source, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: directory, ManifestSHA256: digest}, bundle.Encryption)
	require.NoError(t, err)
	_, request, history := historyPackageFixture(t)
	prepared, err := source.ImportIdentity(request.Operation)
	require.NoError(t, err)
	encoded, err := json.Marshal(prepared, json.Deterministic(true))
	require.NoError(t, err)
	history.BackupSHA256 = digest
	history.DeploymentID = source.DeploymentID()
	history.PreparedSHA256 = historySHA256(encoded)
	history.HighestEpoch.HighestEpoch = prepared.Boundary.Epoch + 3
	history.Interval.Through = manifest.FinishedAt.Add(time.Second)
	history.Disposition = "replay_complete"
	if steps {
		data := []byte("independent recovered bytes")
		require.NoError(t, os.Mkdir(filepath.Join(request.Directory, "assets"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(request.Directory, "assets", "000001.bin"), data, 0o600))
		history.Assets = []historyAsset{{ID: "retained", Path: "assets/000001.bin", Size: int64(len(data)), SHA256: historySHA256(data), Evidence: []string{"source"}}}
		grant := identity.AccountGrant{AccountID: "tenant", UserID: "person", CreatedAt: time.Now().UTC()}
		create, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{After: &grant}}})
		require.NoError(t, err)
		withdraw, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{Before: &grant}}})
		require.NoError(t, err)
		view := historyPayloadView(t, bundle.KV)
		_, kvExpected, err := revision.CaptureKVRecovery(t.Context(), view)
		require.NoError(t, err)
		conn, err := bundle.SQL.Conn(t.Context())
		require.NoError(t, err)
		sqlExpected, err := revision.CaptureSQLRecovery(t.Context(), bundle.SQL, conn)
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		inputs := []struct {
			kind  string
			value any
		}{
			{"blob_publication", historyBlobPayload{Version: 1, Key: "independent-file", Expected: blob.PublicationState{Kind: "absent"}, Next: blob.PublicationState{Kind: "live", Size: int64(len(data)), SHA256: historySHA256(data)}, AssetID: "retained"}},
			{"sql_identity", create}, {"sql_identity", withdraw},
			{"kv_authorization_final", historyKVAuthorityPayload{Version: 1, ExpectedSHA256: kvExpected}},
			{"sql_authorization_final", historySQLAuthorityPayload{Version: 1, Expected: sqlExpected}},
		}
		for i, input := range inputs {
			data := historyPayloadJSON(t, input.value)
			path := fmt.Sprintf("payloads/%06d.json", i+1)
			require.NoError(t, os.WriteFile(filepath.Join(request.Directory, path), data, 0o600))
			history.Steps = append(history.Steps, historyStep{Ordinal: i + 1, Kind: input.kind, Path: path, Size: len(data), SHA256: historySHA256(data), Evidence: []string{"source"}})
		}
	}
	request = writeHistoryManifest(t, request, history)
	verified, err := source.VerifyHistoryPackage(t.Context(), request)
	require.NoError(t, err)
	target, kv, _ := bundleRestoreTargets(t, shared)
	_, err = source.Prepare(t.Context(), target, request.Operation)
	require.NoError(t, err)
	witness, err := New(target.SQL)
	require.NoError(t, err)
	accepted, err := witness.AcceptImportedHistory(t.Context(), source, verified, HistoryAcceptanceRequest{Directory: privateKVDirectory(t), Attestation: HistoryAttestation{Operator: "private-operator", Reference: "private-acceptance-reference", WritersFenced: true, AdmittedWorkAccounted: true, CompleteInterval: true}})
	require.NoError(t, err)
	return historyRunnerFixture{witness: witness, source: source, accepted: accepted, targets: HistoryReplayTargets{KV: target.KV.(HistoryKVTarget), Blobs: target.Blobs.(HistoryBlobTarget), Encryption: bundle.Encryption}, request: HistoryReplayRequest{TargetSHA256: request.TargetSHA256, ScratchDirectory: privateKVDirectory(t)}, kv: kv, packageDirectory: request.Directory}
}
func (f historyRunnerFixture) run(t *testing.T) *CompletedHistory {
	t.Helper()
	result, err := f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
	require.NoError(t, err)
	return result
}
func TestHistoryRunnerNativeFiniteManifestAndRestart(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			f := newHistoryRunnerFixture(t, shared, true)
			result := f.run(t)
			report := result.Report()
			require.Equal(t, 5, report.CompletedSteps)
			require.Equal(t, f.accepted.state.boundary, report.Boundary)
			require.Equal(t, f.request.TargetSHA256, report.TargetSHA256)
			require.True(t, report.KVRotated)
			require.True(t, report.SQLRotated)
			require.True(t, report.Restricted)
			require.Equal(t, int64(1), report.Positions.KV.Sequence)
			require.Equal(t, int64(3), report.Positions.SQL.Sequence)
			require.Equal(t, int64(1), report.Positions.Blobs.Sequence)
			require.NoError(t, result.check(t.Context(), f.witness))
			again := f.run(t)
			require.Equal(t, report, again.Report())
			identity, err := identity.Open(f.witness.db)
			require.NoError(t, err)
			grants, err := identity.AccountGrants.ReachableAccounts(t.Context(), "person")
			require.NoError(t, err)
			require.Empty(t, grants)
			conn, err := f.witness.db.Conn(t.Context())
			require.NoError(t, err)
			stamp, err := revision.CaptureSQLRecovery(t.Context(), f.witness.db, conn)
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			require.Equal(t, historyAcceptedAuthority(f.accepted).Epoch, stamp.Epoch)
			require.Equal(t, uint64(1), stamp.Sequence)
			require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%p"} {
				require.NotContains(t, fmt.Sprintf(verb, *result), "private-operator")
			}
		})
	}
}

var errHistoryLostReply = errors.New("native reply lost")

type historyLostKV struct {
	HistoryKVTarget
	lost bool
}

func (o *historyLostKV) ReconcileImport(ctx context.Context, claim []byte, sequence int64, previous, evidence string, changes []storage.CompareAndSwapMutation) (string, error) {
	receipt, err := o.HistoryKVTarget.ReconcileImport(ctx, claim, sequence, previous, evidence, changes)
	if err == nil && !o.lost {
		o.lost = true
		return "", errHistoryLostReply
	}
	return receipt, err
}

type historyLostBlob struct {
	HistoryBlobTarget
	lost bool
}

func (o *historyLostBlob) ReplayPublication(ctx context.Context, operation string, original blob.Snapshot, step blob.ImportPublicationStep, input io.Reader, scratch string) (string, error) {
	receipt, err := o.HistoryBlobTarget.ReplayPublication(ctx, operation, original, step, input, scratch)
	if err == nil && !o.lost {
		o.lost = true
		return "", errHistoryLostReply
	}
	return receipt, err
}
func TestHistoryRunnerLostNativeRepliesReuseOriginalImages(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, owner := range []string{"kv", "blob"} {
			t.Run(fmt.Sprintf("%t-%s", shared, owner), func(t *testing.T) {
				f := newHistoryRunnerFixture(t, shared, true)
				ordinal := 1
				if owner == "kv" {
					f.targets.KV = &historyLostKV{HistoryKVTarget: f.targets.KV}
					ordinal = 4
				} else {
					f.targets.Blobs = &historyLostBlob{HistoryBlobTarget: f.targets.Blobs}
				}
				_, err := f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
				require.ErrorIs(t, err, errHistoryLostReply)
				path := filepath.Join(f.accepted.state.directory, historyStepName(ordinal, "prepared"))
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				_, err = os.Stat(filepath.Join(f.accepted.state.directory, historyStepName(ordinal, "applied")))
				require.ErrorIs(t, err, os.ErrNotExist)
				result := f.run(t)
				require.Equal(t, 5, result.Report().CompletedSteps)
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}
func TestHistoryRunnerEmptyIntervalStaysRestricted(t *testing.T) {
	f := newHistoryRunnerFixture(t, false, false)
	result := f.run(t)
	require.Zero(t, result.Report().CompletedSteps)
	require.False(t, result.Report().KVRotated)
	require.False(t, result.Report().SQLRotated)
	require.True(t, result.Report().Restricted)
	require.NoError(t, result.check(t.Context(), f.witness))
	require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
}
func TestHistoryRunnerRejectsRotationOrder(t *testing.T) {
	for _, kinds := range [][]string{{"kv_authorization_final", "kv_domain"}, {"sql_authorization_final", "sql_identity"}, {"kv_authorization_final", "kv_authorization_final"}} {
		var steps []historyStep
		for i, kind := range kinds {
			steps = append(steps, historyStep{Ordinal: i + 1, Kind: kind})
		}
		require.Error(t, checkHistoryRotationOrder(steps))
	}
	require.NoError(t, checkHistoryRotationOrder([]historyStep{{Kind: "kv_authorization_final"}, {Kind: "sql_identity"}, {Kind: "sql_authorization_final"}}))
}
func TestHistoryRunnerRejectsChangedInputsAndRetainedEvidence(t *testing.T) {
	for _, change := range []string{"target", "acceptance", "header", "prepared", "applied", "image", "missing-header", "future"} {
		t.Run(change, func(t *testing.T) {
			f := newHistoryRunnerFixture(t, false, true)
			result := f.run(t)
			base := f.accepted.state.directory
			switch change {
			case "target":
				f.request.TargetSHA256 = strings.Repeat("f", 64)
			case "acceptance":
				require.NoError(t, os.WriteFile(filepath.Join(base, "acceptance.json"), []byte("{}"), 0o600))
			case "header":
				require.NoError(t, os.WriteFile(filepath.Join(base, "runner.json"), []byte("{}"), 0o600))
			case "prepared", "applied":
				require.NoError(t, os.WriteFile(filepath.Join(base, historyStepName(1, change)), []byte("{}"), 0o600))
			case "missing-header":
				require.NoError(t, os.Remove(filepath.Join(base, "runner.json")))
			case "future":
				require.NoError(t, os.WriteFile(filepath.Join(base, historyStepName(6, "applied")), []byte("{}"), 0o600))
			case "image":
				var p historyPreparedStep
				_, err := result.runner.readJournalRecord(t.Context(), historyStepName(1, "prepared"), &p)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(result.runner.imagePath(p), "blobs.tar"), []byte("corrupt"), 0o600))
			}
			_, err := f.witness.ReplayImportedHistory(t.Context(), f.source, f.accepted, f.targets, f.request)
			require.Error(t, err)
			if change != "target" {
				require.Error(t, result.check(t.Context(), f.witness))
			}
			require.ErrorIs(t, f.witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
		})
	}
}
