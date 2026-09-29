package app

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// verifyCapturedVideoCorrection uses the actual production transaction and portable image.
func verifyCapturedVideoCorrection(t *testing.T, application *App, account, id string) {
	t.Helper()
	source, err := storage.OpenRecordTransfer(t.Context(), application.store, "")
	require.NoError(t, err)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Chmod(parent, 0700))
	directory := filepath.Join(parent, "snapshot")
	receipt, err := recovery.SnapshotKV(t.Context(), source, directory)
	require.NoError(t, err)
	scratch, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Chmod(scratch, 0700))
	image, err := recovery.OpenKVSnapshot(t.Context(), recovery.KVSnapshotPath(directory), scratch, receipt)
	require.NoError(t, err)
	defer func() { require.NoError(t, image.Close()) }()
	job, err := jobs.ReadRecoveryJob(t.Context(), image, account, id)
	require.NoError(t, err)
	execution, err := jobs.VerifyRecoveryExecution(t.Context(), image, job)
	require.NoError(t, err)
	require.Equal(t, jobs.RecoveryExecution{ReservedJobs: 1}, execution)
	corrections, err := jobs.VerifyRecoveryCorrections(t.Context(), image, job)
	require.NoError(t, err)
	require.Equal(t, jobs.RecoveryCorrections{Intents: 1, Applied: 1, Reported: 1}, corrections)
	for _, mode := range []string{"missing-budget-receipt", "different-actor", "missing-publication", "different-evidence"} {
		t.Run(mode, func(t *testing.T) {
			source := changedCorrectionReceipt{BackupReader: image, mode: mode}
			_, err := jobs.VerifyRecoveryCorrections(t.Context(), source, job)
			require.Error(t, err)
		})
	}
}

type changedCorrectionReceipt struct {
	reservation.BackupReader
	mode string
}

func (s changedCorrectionReceipt) ReadCaptured(ctx context.Context, key string, bound int) (storage.TransferRecord, error) {
	record, err := s.BackupReader.ReadCaptured(ctx, key, bound)
	if err != nil || !strings.HasPrefix(key, reservation.StoragePrefix+"correction:") {
		return record, err
	}
	if s.mode == "missing-budget-receipt" {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	var receipt reservation.CorrectionReceipt
	if err := json.Unmarshal(record.Value, &receipt); err != nil {
		return record, err
	}
	switch s.mode {
	case "different-actor":
		receipt.Correction.Actor = "key:different-operator"
	case "missing-publication":
		receipt.PublicationDigest = ""
	case "different-evidence":
		receipt.Correction.Evidence.ID = "different-provider-evidence"
	}
	record.Value, err = json.Marshal(receipt)
	return record, err
}
