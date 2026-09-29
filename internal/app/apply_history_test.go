package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func TestApplyImportedHistoryRetainsClosedIdempotentJournal(t *testing.T) {
	cfg, inspection, identity := importedInspectionFixture(t)
	checked, err := InspectImportedBackup(t.Context(), cfg, inspection)
	require.NoError(t, err)
	parent := filepath.Dir(inspection.Directory)
	history, journal := filepath.Join(parent, "independent-history"), filepath.Join(parent, "replay-journal")
	require.NoError(t, os.Mkdir(history, 0o700))
	require.NoError(t, os.Mkdir(journal, 0o700))
	prepared, err := json.Marshal(identity, json.Deterministic(true))
	require.NoError(t, err)
	preparedSHA := sha256.Sum256(prepared)
	manifest := map[string]any{
		"version": 1, "backup_sha256": inspection.ManifestSHA256, "deployment_id": inspection.ExpectedBoundary.DeploymentID,
		"operation": inspection.Operation, "target_sha256": checked.TargetSHA256, "prepared_sha256": hex.EncodeToString(preparedSHA[:]),
		"mode": "disaster_recovery", "disposition": "remain_restricted",
		"interval":         map[string]any{"through_utc": time.Now().UTC().Add(time.Minute), "end_reference": "independent-cutoff"},
		"highest_epoch":    recovery.EpochEvidence{HighestEpoch: inspection.ExpectedBoundary.Epoch + 10, SourceSHA256: strings.Repeat("e", 64), Reference: "external-epoch-record", Operator: "operator"},
		"evidence_sources": []map[string]any{{"id": "external", "sha256": strings.Repeat("e", 64), "size": 12, "reference": "external-archive"}},
		"steps":            []any{},
	}
	body, err := json.Marshal(manifest, json.Deterministic(true))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(history, "history.json"), body, 0o600))
	sum := sha256.Sum256(body)
	request := recovery.ApplyHistoryRequest{VerifyRequest: inspection.VerifyRequest, Operation: inspection.Operation, HistoryDirectory: history, HistorySHA256: hex.EncodeToString(sum[:]), ExpectedTargetSHA256: checked.TargetSHA256, JournalDirectory: journal, Attestation: recovery.HistoryAttestation{Operator: "operator", Reference: "independent-history", WritersFenced: true, AdmittedWorkAccounted: true}}

	wrong := request
	wrong.ExpectedTargetSHA256 = strings.Repeat("0", 64)
	_, err = ApplyImportedHistory(t.Context(), cfg, wrong)
	require.ErrorIs(t, err, recovery.ErrConflict)
	require.NoFileExists(t, filepath.Join(journal, "acceptance.json"))
	wrong = request
	wrong.JournalDirectory = history
	_, err = ApplyImportedHistory(t.Context(), cfg, wrong)
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(history, "acceptance.json"))

	first, err := ApplyImportedHistory(t.Context(), cfg, request)
	require.NoError(t, err)
	require.True(t, first.Restricted)
	require.Zero(t, first.CompletedSteps)
	require.False(t, first.KVRotated)
	require.False(t, first.SQLRotated)
	require.Len(t, first.AcceptanceSHA256, 64)
	second, err := ApplyImportedHistory(t.Context(), cfg, request)
	require.NoError(t, err)
	require.Equal(t, first, second)
	assertInspectionStillClosed(t, cfg)
	wrong = request
	wrong.Attestation.Reference = "changed-external-evidence"
	_, err = ApplyImportedHistory(t.Context(), cfg, wrong)
	require.Error(t, err)
	assertInspectionStillClosed(t, cfg)
}
