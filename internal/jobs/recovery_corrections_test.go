package jobs_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type capturedJobRecords map[string]storage.TransferRecord

func (s capturedJobRecords) ReadCaptured(ctx context.Context, key string, bound int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	record, ok := s[key]
	if !ok {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	if len(record.Value) > bound {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return record, nil
}

func captureJobRecords(t *testing.T, store storage.KVStore) capturedJobRecords {
	t.Helper()
	identity := ""
	if provider, ok := store.(storage.IncarnationProvider); ok {
		var err error
		identity, err = provider.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	source, err := storage.OpenRecordTransfer(t.Context(), store, identity)
	require.NoError(t, err)
	result := make(capturedJobRecords)
	require.NoError(t, source.Enumerate(t.Context(), func(record storage.TransferRecord) error {
		record.Value = bytes.Clone(record.Value)
		result[record.Key] = record
		return nil
	}))
	return result
}

func TestRecoveryCorrectionChainsKeepPendingAndReportingState(t *testing.T) {
	store, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, Compression: "snappy", NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	records, job := correctionJob(t, store)
	// The first intent remains superseded. Only the next two change billing.
	first := correctionIntent(t, job, "superseded")
	job, err = records.CreateCorrection(t.Context(), job, first)
	require.NoError(t, err)
	for _, id := range []string{"applied-one", "applied-two"} {
		intent := correctionIntent(t, job, id)
		job, err = records.CreateCorrection(t.Context(), job, intent)
		require.NoError(t, err)
		job, err = records.ApplyCorrection(t.Context(), job, nil)
		require.NoError(t, err)
		if id == "applied-one" {
			job, err = records.MarkCorrectionReported(t.Context(), job, intent, "expired")
			require.NoError(t, err)
		}
	}
	pending := correctionIntent(t, job, "pending")
	job, err = records.CreateCorrection(t.Context(), job, pending)
	require.NoError(t, err)
	source := captureJobRecords(t, store)
	report, err := jobs.VerifyRecoveryCorrections(t.Context(), source, job)
	require.NoError(t, err)
	require.Equal(t, jobs.RecoveryCorrections{Intents: 4, Applied: 2, Reported: 1}, report)
	counts := make(map[string]int)
	for _, record := range source {
		if !strings.HasPrefix(record.Key, jobs.CorrectionStoragePrefix) {
			continue
		}
		kind, err := jobs.VerifyRecoveryCorrectionRecord(t.Context(), source, record)
		require.NoError(t, err)
		counts[kind]++
	}
	require.Equal(t, map[string]int{"intent": 4, "history-next": 4, "applied": 2, "applied-next": 2, "reported": 1}, counts)
	after, err := records.Get(t.Context(), job.Account, job.ID)
	require.NoError(t, err)
	require.Equal(t, job, after, "inspection cannot apply pending billing or advance reports")
	for _, mode := range []string{"missing-intent", "missing-history-link", "missing-applied", "missing-applied-link", "missing-report", "expiring-record", "changed-marker"} {
		t.Run(mode, func(t *testing.T) {
			corrupted := make(capturedJobRecords, len(source))
			for key, record := range source {
				record.Value = bytes.Clone(record.Value)
				corrupted[key] = record
			}
			needle := map[string]string{"missing-intent": ":intent:", "missing-history-link": ":history-next:", "missing-applied": ":applied:", "missing-applied-link": ":applied-next:", "missing-report": ":reported:", "expiring-record": ":intent:", "changed-marker": ":applied:"}[mode]
			changed := false
			for key, record := range corrupted {
				if !strings.HasPrefix(key, jobs.CorrectionStoragePrefix) || !strings.Contains(key, needle) {
					continue
				}
				switch mode {
				case "expiring-record":
					record.ExpiresAtMillis = 1
					corrupted[key] = record
				case "changed-marker":
					record.Value = []byte(`{"version":1}`)
					corrupted[key] = record
				default:
					delete(corrupted, key)
				}
				changed = true
				break
			}
			require.True(t, changed)
			_, err := jobs.VerifyRecoveryCorrections(t.Context(), corrupted, job)
			require.Error(t, err)
		})
	}
}
