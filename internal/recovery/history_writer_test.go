package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const historyWriterKVMarker = `{"epoch":"backup-epoch","sequence":3}`

func historyWriterFixture(t *testing.T) (*RestoreSource, HistoryWriteRequest) {
	t.Helper()
	bundle, capture, _ := backupBundleFixture(t)
	kv, records, _ := kvTransferStores(t, storage.StorageTypeBadger)
	bundle.KV = records
	require.NoError(t, kv.Set(t.Context(), revision.StorageKey, []byte(historyWriterKVMarker)))
	source := captureHistoryProjectionBundle(t, bundle, capture)
	evidence := filepath.Join(privateKVDirectory(t), "source-stopped.log")
	require.NoError(t, os.WriteFile(evidence, []byte("source stopped"), 0o600))
	epoch := filepath.Join(privateKVDirectory(t), "epoch.log")
	require.NoError(t, os.WriteFile(epoch, []byte("highest epoch record"), 0o600))
	return source, HistoryWriteRequest{
		Directory: privateKVDirectory(t), Operation: RestoreOperation{ID: "restore-operation", FencingEvidence: "private-fence-reference"},
		TargetSHA256: strings.Repeat("b", 64), SQLExpected: &revision.Stamp{Epoch: "target-epoch", Sequence: 4},
		Mode: "planned_migration", Disposition: historyReplayComplete, Through: source.manifest.FinishedAt.Add(time.Second), EndReference: "source-stopped",
		HighestEpoch: source.manifest.Request.Boundary.Epoch + 4, EpochReference: "independent-epoch", EpochOperator: "operator", EpochEvidence: "epoch",
		Evidence:    []HistoryEvidenceFile{{ID: "source", Path: evidence, Reference: "private-history-reference"}, {ID: "epoch", Path: epoch, Reference: "private-epoch-reference"}},
		Attestation: HistoryAttestation{Operator: "operator", Reference: "independent-complete-interval", WritersFenced: true, AdmittedWorkAccounted: true, CompleteInterval: true},
	}
}

func historyWriterEntries(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestWriteFinalHistoryRoundTripsThroughVerification(t *testing.T) {
	source, request := historyWriterFixture(t)
	written, err := source.WriteFinalHistory(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, 2, written.StepCount())
	body, err := os.ReadFile(filepath.Join(request.Directory, "history.json"))
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	require.Equal(t, hex.EncodeToString(sum[:]), written.Digest())
	verified, err := source.VerifyHistoryPackage(t.Context(), HistoryPackageRequest{Directory: request.Directory, ManifestSHA256: written.Digest(), TargetSHA256: request.TargetSHA256, Operation: request.Operation})
	require.NoError(t, err)
	manifest := verified.state.manifest
	stop, err := catalogPrefixCount(manifest.Steps)
	require.NoError(t, err)
	require.Zero(t, stop, "the writer emits only the final authority rotations")
	require.Equal(t, source.ManifestDigest(), manifest.BackupSHA256)
	require.Equal(t, source.DeploymentID(), manifest.DeploymentID)
	require.Equal(t, request.Through.UTC(), manifest.Interval.Through)
	epoch, err := os.ReadFile(request.Evidence[1].Path)
	require.NoError(t, err)
	epochSum := sha256.Sum256(epoch)
	require.Equal(t, hex.EncodeToString(epochSum[:]), manifest.HighestEpoch.SourceSHA256)
	require.Equal(t, int64(len(epoch)), manifest.Evidence[1].Size)
	for _, step := range manifest.Steps {
		require.Equal(t, []string{"source", "epoch"}, step.Evidence)
	}
	var kvFinal historyKVAuthorityPayload
	require.NoError(t, json.Unmarshal(verified.state.payloads[0], &kvFinal, json.RejectUnknownMembers(true)))
	marker := sha256.Sum256([]byte(historyWriterKVMarker))
	require.Equal(t, hex.EncodeToString(marker[:]), kvFinal.ExpectedSHA256, "the KV expectation comes from the backup capture")
	var sqlFinal historySQLAuthorityPayload
	require.NoError(t, json.Unmarshal(verified.state.payloads[1], &sqlFinal, json.RejectUnknownMembers(true)))
	require.Equal(t, request.SQLExpected, sqlFinal.Expected)
	// The attestation authorizes the write and is not part of the package.
	require.NotContains(t, string(body), request.Attestation.Reference)
	require.NotContains(t, string(body), "writers_fenced")
}

func TestWriteFinalHistoryRecordsExplicitSQLAbsence(t *testing.T) {
	source, request := historyWriterFixture(t)
	request.SQLExpected = nil
	request.Disposition = "remain_restricted"
	written, err := source.WriteFinalHistory(t.Context(), request)
	require.NoError(t, err)
	require.True(t, explicitHistoryMembers(written.state.payloads[1], "expected"))
}

func TestWriteFinalHistoryManifestBytesAreDeterministic(t *testing.T) {
	source, request := historyWriterFixture(t)
	first, err := source.WriteFinalHistory(t.Context(), request)
	require.NoError(t, err)
	again := request
	again.Directory = privateKVDirectory(t)
	again.Through = request.Through.In(time.FixedZone("operator", -7*60*60))
	second, err := source.WriteFinalHistory(t.Context(), again)
	require.NoError(t, err)
	require.Equal(t, first.Digest(), second.Digest())
	for _, name := range []string{"history.json", "payloads/000001.json", "payloads/000002.json"} {
		want, err := os.ReadFile(filepath.Join(request.Directory, name))
		require.NoError(t, err)
		got, err := os.ReadFile(filepath.Join(again.Directory, name))
		require.NoError(t, err)
		require.Equal(t, want, got, name)
	}
}

func TestWriteFinalHistoryRefusesBeforeWriting(t *testing.T) {
	for name, change := range map[string]func(*HistoryWriteRequest){
		"unfenced":             func(r *HistoryWriteRequest) { r.Attestation.WritersFenced = false },
		"unaccounted":          func(r *HistoryWriteRequest) { r.Attestation.AdmittedWorkAccounted = false },
		"incomplete-interval":  func(r *HistoryWriteRequest) { r.Attestation.CompleteInterval = false },
		"attestation-operator": func(r *HistoryWriteRequest) { r.Attestation.Operator = " " },
		"attestation-ref":      func(r *HistoryWriteRequest) { r.Attestation.Reference = "" },
		"relative-directory":   func(r *HistoryWriteRequest) { r.Directory = "relative" },
		"operation":            func(r *HistoryWriteRequest) { r.Operation.ID = "" },
		"fence":                func(r *HistoryWriteRequest) { r.Operation.FencingEvidence = "" },
		"mode":                 func(r *HistoryWriteRequest) { r.Mode = "automatic" },
		"disposition":          func(r *HistoryWriteRequest) { r.Disposition = "activate" },
		"zero-through":         func(r *HistoryWriteRequest) { r.Through = time.Time{} },
		"through-before":       func(r *HistoryWriteRequest) { r.Through = r.Through.Add(-time.Hour) },
		"end-reference":        func(r *HistoryWriteRequest) { r.EndReference = "line\nbreak" },
		"epoch-below":          func(r *HistoryWriteRequest) { r.HighestEpoch = -1 },
		"epoch-boundary":       func(r *HistoryWriteRequest) { r.HighestEpoch = 0 },
		"epoch-reference":      func(r *HistoryWriteRequest) { r.EpochReference = "" },
		"epoch-operator":       func(r *HistoryWriteRequest) { r.EpochOperator = "" },
		"epoch-evidence":       func(r *HistoryWriteRequest) { r.EpochEvidence = "unknown" },
		"no-evidence":          func(r *HistoryWriteRequest) { r.Evidence = nil },
		"duplicate-evidence":   func(r *HistoryWriteRequest) { r.Evidence[1].ID = r.Evidence[0].ID },
		"evidence-reference":   func(r *HistoryWriteRequest) { r.Evidence[0].Reference = "" },
		"relative-evidence":    func(r *HistoryWriteRequest) { r.Evidence[0].Path = "source-stopped.log" },
		"target":               func(r *HistoryWriteRequest) { r.TargetSHA256 = strings.Repeat("B", 64) },
		"sql-preimage":         func(r *HistoryWriteRequest) { r.SQLExpected = &revision.Stamp{Epoch: "target-epoch"} },
	} {
		t.Run(name, func(t *testing.T) {
			source, request := historyWriterFixture(t)
			change(&request)
			_, err := source.WriteFinalHistory(t.Context(), request)
			require.ErrorIs(t, err, ErrConflict)
			if filepath.IsAbs(request.Directory) {
				require.Empty(t, historyWriterEntries(t, request.Directory))
			}
		})
	}
}

func TestWriteFinalHistoryRefusesUnsafeEvidenceAndDirectories(t *testing.T) {
	t.Run("missing-evidence", func(t *testing.T) {
		source, request := historyWriterFixture(t)
		request.Evidence[0].Path = filepath.Join(filepath.Dir(request.Evidence[0].Path), "absent.log")
		_, err := source.WriteFinalHistory(t.Context(), request)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.Empty(t, historyWriterEntries(t, request.Directory))
	})
	t.Run("linked-evidence", func(t *testing.T) {
		source, request := historyWriterFixture(t)
		link := filepath.Join(filepath.Dir(request.Evidence[0].Path), "linked.log")
		require.NoError(t, os.Symlink(request.Evidence[0].Path, link))
		request.Evidence[0].Path = link
		_, err := source.WriteFinalHistory(t.Context(), request)
		require.ErrorContains(t, err, "not a regular file")
		require.Empty(t, historyWriterEntries(t, request.Directory))
	})
	t.Run("populated-directory", func(t *testing.T) {
		source, request := historyWriterFixture(t)
		require.NoError(t, os.WriteFile(filepath.Join(request.Directory, "operator-notes"), []byte("keep"), 0o600))
		_, err := source.WriteFinalHistory(t.Context(), request)
		require.ErrorIs(t, err, ErrConflict)
		require.Equal(t, []string{"operator-notes"}, historyWriterEntries(t, request.Directory))
	})
	t.Run("missing-directory", func(t *testing.T) {
		source, request := historyWriterFixture(t)
		request.Directory = filepath.Join(request.Directory, "absent")
		_, err := source.WriteFinalHistory(t.Context(), request)
		require.ErrorIs(t, err, ErrConflict)
		require.NoDirExists(t, request.Directory)
	})
	t.Run("nil-source", func(t *testing.T) {
		_, request := historyWriterFixture(t)
		var source *RestoreSource
		_, err := source.WriteFinalHistory(t.Context(), request)
		require.ErrorIs(t, err, ErrConflict)
	})
	t.Run("canceled", func(t *testing.T) {
		source, request := historyWriterFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := source.WriteFinalHistory(ctx, request)
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, historyWriterEntries(t, request.Directory))
	})
}

func TestWriteFinalHistoryInterruptionLeavesNoManifest(t *testing.T) {
	for _, stop := range []string{"payloads/000001.json", "payloads/000002.json", "history.json"} {
		t.Run(stop, func(t *testing.T) {
			source, request := historyWriterFixture(t)
			interrupted := errors.New("interrupted")
			_, err := source.writeFinalHistory(t.Context(), request, func(name string) error {
				if name == stop {
					return interrupted
				}
				return nil
			})
			require.ErrorIs(t, err, interrupted)
			require.NoFileExists(t, filepath.Join(request.Directory, "history.json"))
			require.DirExists(t, filepath.Join(request.Directory, "payloads"))
			// The partial directory is not empty, so an exact retry refuses until the operator removes it.
			_, err = source.WriteFinalHistory(t.Context(), request)
			require.ErrorIs(t, err, ErrConflict)
			require.NoFileExists(t, filepath.Join(request.Directory, "history.json"))
		})
	}
}

func TestWriteHistoryRequestLeavesDerivedBindingsToTheCommand(t *testing.T) {
	_, history := historyWriterFixture(t)
	history.TargetSHA256, history.SQLExpected = "", nil
	request := WriteHistoryRequest{VerifyRequest: VerifyRequest{Directory: privateKVDirectory(t), ManifestSHA256: strings.Repeat("a", 64)}, History: history}
	require.NoError(t, request.Validate())
	for name, change := range map[string]func(*WriteHistoryRequest){
		"backup-digest":   func(r *WriteHistoryRequest) { r.ManifestSHA256 = "invalid" },
		"history":         func(r *WriteHistoryRequest) { r.History.Attestation.CompleteInterval = false },
		"derived-target":  func(r *WriteHistoryRequest) { r.History.TargetSHA256 = strings.Repeat("b", 64) },
		"derived-sql":     func(r *WriteHistoryRequest) { r.History.SQLExpected = &revision.Stamp{Epoch: "epoch", Sequence: 1} },
		"expected-target": func(r *WriteHistoryRequest) { r.ExpectedTargetSHA256 = "invalid" },
		"incarnation":     func(r *WriteHistoryRequest) { r.ValkeyIncarnation = "run\nid" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			changed.History.Evidence = append([]HistoryEvidenceFile(nil), request.History.Evidence...)
			change(&changed)
			require.Error(t, changed.Validate())
		})
	}
}
