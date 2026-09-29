package recovery

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func historyAcceptanceFixture(t *testing.T, shared bool) (*Witness, *RestoreSource, *VerifiedHistory, HistoryAcceptanceRequest) {
	t.Helper()
	bundle, capture, directory := backupBundleRecipe(t, shared)
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
	request = writeHistoryManifest(t, request, history)
	verified, err := source.VerifyHistoryPackage(t.Context(), request)
	require.NoError(t, err)
	target, _, _ := bundleRestoreTargets(t, shared)
	_, err = source.Prepare(t.Context(), target, request.Operation)
	require.NoError(t, err)
	witness, err := New(target.SQL)
	require.NoError(t, err)
	accept := HistoryAcceptanceRequest{Directory: privateKVDirectory(t), Attestation: HistoryAttestation{Operator: "private-operator", Reference: "private-acceptance-reference", WritersFenced: true, AdmittedWorkAccounted: true}}
	return witness, source, verified, accept
}

func TestHistoryAcceptanceRetainsClosedSQLAndExactRetry(t *testing.T) {
	witness, source, verified, request := historyAcceptanceFixture(t, false)
	accepted, err := witness.AcceptImportedHistory(t.Context(), source, verified, request)
	require.NoError(t, err)
	require.NotEmpty(t, accepted.Digest())
	require.False(t, accepted.state.boundary.Open)
	require.Greater(t, accepted.state.boundary.Epoch, verified.state.manifest.HighestEpoch.HighestEpoch)
	require.False(t, accepted.state.history.manifest.Disposition == "replay_complete")
	require.ErrorIs(t, witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	_, err = witness.Approved(t.Context(), source.DeploymentID())
	require.ErrorIs(t, err, ErrClosed)
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%p", "%+p", "%#p"} {
		diagnostic := fmt.Sprintf(format, *accepted)
		require.NotContains(t, diagnostic, "private-operator")
		require.NotContains(t, diagnostic, "private-acceptance-reference")
	}
	restarted, err := New(witness.db)
	require.NoError(t, err)
	again, err := restarted.AcceptImportedHistory(t.Context(), source, verified, request)
	require.NoError(t, err)
	require.Equal(t, accepted.Digest(), again.Digest())
	require.Equal(t, accepted.state.boundary, again.state.boundary)
	// The retained source and caller values cannot alter the accepted journal.
	request.Attestation.Reference = "different"
	_, err = restarted.AcceptImportedHistory(t.Context(), source, verified, request)
	require.Error(t, err)
	require.NoError(t, accepted.check(t.Context(), witness))
}

func TestHistoryAcceptanceResumesAfterJournalPublication(t *testing.T) {
	witness, source, verified, request := historyAcceptanceFixture(t, false)
	prepared, err := prepareHistoryAcceptance(source, verified, request)
	require.NoError(t, err)
	require.NoError(t, retainHistoryAcceptance(t.Context(), prepared, true))
	current, err := witness.Current(t.Context(), source.DeploymentID())
	require.NoError(t, err)
	require.Equal(t, prepared.epoch.Prepared, current, "private publication alone cannot advance SQL or open admission")
	accepted, err := witness.AcceptImportedHistory(t.Context(), source, verified, request)
	require.NoError(t, err)
	require.Equal(t, prepared.digest, accepted.Digest())
	require.NoError(t, accepted.check(t.Context(), witness))
}

func TestHistoryAcceptanceRefusesChangedJournalAndLaterBoundary(t *testing.T) {
	for _, change := range []string{"journal", "missing", "epoch"} {
		t.Run(change, func(t *testing.T) {
			witness, source, verified, request := historyAcceptanceFixture(t, false)
			accepted, err := witness.AcceptImportedHistory(t.Context(), source, verified, request)
			require.NoError(t, err)
			switch change {
			case "journal":
				require.NoError(t, os.WriteFile(filepath.Join(request.Directory, "acceptance.json"), []byte(`{}`), 0o600))
			case "missing":
				require.NoError(t, os.Remove(filepath.Join(request.Directory, "acceptance.json")))
			case "epoch":
				_, err = witness.Close(t.Context(), accepted.state.boundary)
				require.NoError(t, err)
			}
			require.Error(t, accepted.check(t.Context(), witness))
			_, err = witness.AcceptImportedHistory(t.Context(), source, verified, request)
			require.Error(t, err)
			require.ErrorIs(t, witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
		})
	}
}

func TestHistoryAcceptanceRequiresExplicitExternalFacts(t *testing.T) {
	witness, source, verified, request := historyAcceptanceFixture(t, false)
	for name, change := range map[string]func(*HistoryAcceptanceRequest){
		"operator":      func(r *HistoryAcceptanceRequest) { r.Attestation.Operator = "" },
		"reference":     func(r *HistoryAcceptanceRequest) { r.Attestation.Reference = "" },
		"fence":         func(r *HistoryAcceptanceRequest) { r.Attestation.WritersFenced = false },
		"admitted-work": func(r *HistoryAcceptanceRequest) { r.Attestation.AdmittedWorkAccounted = false },
		"relative":      func(r *HistoryAcceptanceRequest) { r.Directory = "relative" },
		"control":       func(r *HistoryAcceptanceRequest) { r.Attestation.Reference = "line\nbreak" },
		"oversized":     func(r *HistoryAcceptanceRequest) { r.Attestation.Operator = strings.Repeat("x", 4097) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			change(&candidate)
			_, err := witness.AcceptImportedHistory(t.Context(), source, verified, candidate)
			require.Error(t, err)
		})
	}
	_, err := os.Stat(filepath.Join(request.Directory, "acceptance.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, historyRequest, _ := historyPackageFixture(t)
	manifest := verified.state.manifest
	manifest.Disposition = "replay_complete"
	historyRequest.Operation = manifest.Operation
	historyRequest.TargetSHA256 = manifest.TargetSHA256
	historyRequest = writeHistoryManifest(t, historyRequest, manifest)
	verified, err = source.VerifyHistoryPackage(t.Context(), historyRequest)
	require.NoError(t, err)
	_, err = witness.AcceptImportedHistory(t.Context(), source, verified, request)
	require.Error(t, err, "an empty step list is not proof of an empty later interval")
	request.Attestation.CompleteInterval = true
	accepted, err := witness.AcceptImportedHistory(t.Context(), source, verified, request)
	require.NoError(t, err, "explicit external acceptance does not require an invented replay step")
	require.NoError(t, accepted.check(t.Context(), witness))
	require.ErrorIs(t, witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
}

func TestHistoryAcceptanceSharedNative(t *testing.T) {
	witness, source, verified, request := historyAcceptanceFixture(t, true)
	accepted, err := witness.AcceptImportedHistory(t.Context(), source, verified, request)
	require.NoError(t, err)
	again, err := witness.AcceptImportedHistory(t.Context(), source, verified, request)
	require.NoError(t, err)
	require.Equal(t, accepted.Digest(), again.Digest())
	require.NoError(t, accepted.check(t.Context(), witness))
	require.ErrorIs(t, witness.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
}
