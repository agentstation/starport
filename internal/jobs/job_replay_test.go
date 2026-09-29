package jobs_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func videoReplayStore(t *testing.T, backend string) storage.KVStore {
	t.Helper()
	var store storage.KVStore
	var err error
	if backend == "badger" {
		store, err = storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
	} else {
		address := os.Getenv("TEST_VALKEY_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
		}
		store, err = storage.OpenValkey(storage.ValkeyConfig{URL: address, DeploymentID: "video-replay-" + rand.Text(), AllowInsecure: true})
	}
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func videoReplayTransfer(t *testing.T, store storage.KVStore) storage.RecordTransfer {
	t.Helper()
	identity := ""
	if provider, ok := store.(storage.IncarnationProvider); ok {
		var err error
		identity, err = provider.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	transfer, err := storage.OpenRecordTransfer(t.Context(), store, identity)
	require.NoError(t, err)
	return transfer
}

func TestVideoJobReplayStagesLongPrivateCorrectionHistory(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			source := videoReplayStore(t, backend)
			records, job := correctionJob(t, source)
			originalTerminal := job.TerminalAt
			var before capturedJobRecords
			var audit []jobs.RecoveryJobCorrection
			for i := range 70 {
				intent := correctionIntent(t, job, fmt.Sprintf("correction-%03d", i))
				var err error
				job, err = records.CreateCorrection(t.Context(), job, intent)
				require.NoError(t, err)
				job, err = records.ApplyCorrection(t.Context(), job, nil)
				require.NoError(t, err)
				status := ""
				if i < 10 {
					status = "delivered"
					job, err = records.MarkCorrectionReported(t.Context(), job, intent, status)
					require.NoError(t, err)
				}
				evidence, err := jobs.NewRecoveryJobCorrection(intent, true, status)
				require.NoError(t, err)
				audit = append(audit, evidence)
				if i == 7 {
					before = captureJobRecords(t, source)
				}
			}
			finalRecords := captureJobRecords(t, source)
			final, err := jobs.CaptureRecoveryJob(t.Context(), finalRecords, job.Account, job.ID)
			require.NoError(t, err)
			encoded, err := json.Marshal(final)
			require.NoError(t, err)
			require.Contains(t, string(encoded), "native_receipt_key")
			require.Contains(t, string(encoded), "correction_applied")
			require.Equal(t, "<private job recovery evidence>", fmt.Sprintf("%v", final))
			require.Equal(t, "<private job recovery evidence>", fmt.Sprintf("%#v", final))
			var decoded jobs.RecoveryJob
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			target := videoReplayStore(t, backend)
			transfer := videoReplayTransfer(t, target)
			claim := []byte("video-job-replay")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			for _, record := range before {
				require.NoError(t, transfer.Import(t.Context(), claim, record))
			}
			assets, err := blob.NewFilesystem(t.TempDir())
			require.NoError(t, err)
			_, err = jobs.PrepareJobReplay(t.Context(), before, before, assets, time.Now(), decoded)
			require.Error(t, err, "final publication must refuse missing later correction history")
			reconcile := transfer.(storage.ImportReconciler)
			closed := maps.Clone(before)
			receipt := ""
			sequence := int64(0)
			for start := 0; start < len(audit); start += 16 {
				stepBefore := maps.Clone(closed)
				end := min(start+16, len(audit))
				changes, err := jobs.PrepareJobCorrectionReplay(t.Context(), stepBefore, decoded, audit[start:end])
				require.NoError(t, err)
				require.LessOrEqual(t, len(changes), 80)
				retry, err := jobs.PrepareJobCorrectionReplay(t.Context(), stepBefore, decoded, audit[start:end])
				require.NoError(t, err)
				require.Equal(t, changes, retry)
				sequence++
				prior := receipt
				receipt, err = reconcile.ReconcileImport(t.Context(), claim, sequence, prior, strings.Repeat("a", 64), changes)
				require.NoError(t, err)
				replayed, err := reconcile.ReconcileImport(t.Context(), claim, sequence, prior, strings.Repeat("a", 64), retry)
				require.NoError(t, err)
				require.Equal(t, receipt, replayed)
				for _, change := range changes {
					closed[change.Key] = storage.TransferRecord{Key: change.Key, Value: bytes.Clone(change.NewValue)}
				}
				require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
			}
			after := maps.Clone(closed)
			mutation, err := jobs.PrepareJobReplay(t.Context(), before, after, assets, time.Now(), decoded)
			require.NoError(t, err)
			repeated, err := jobs.PrepareJobReplay(t.Context(), before, after, assets, time.Now(), decoded)
			require.NoError(t, err)
			require.Equal(t, mutation, repeated)
			_, err = reconcile.ReconcileImport(t.Context(), claim, sequence+1, receipt, strings.Repeat("b", 64), []storage.CompareAndSwapMutation{mutation})
			require.NoError(t, err)
			raw, err := target.Get(t.Context(), mutation.Key)
			require.NoError(t, err)
			require.Equal(t, mutation.NewValue, raw)
			closed[mutation.Key] = storage.TransferRecord{Key: mutation.Key, Value: raw}
			restored, err := jobs.ReadRecoveryJob(t.Context(), closed, job.Account, job.ID)
			require.NoError(t, err)
			require.Equal(t, job, restored)
			require.Equal(t, originalTerminal, restored.TerminalAt)
			report, err := jobs.VerifyRecoveryCorrections(t.Context(), closed, restored)
			require.NoError(t, err)
			require.Equal(t, jobs.RecoveryCorrections{Intents: 70, Applied: 70, Reported: 10}, report)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
		})
	}
}

func TestVideoJobReplayRejectsRetainedStateLoss(t *testing.T) {
	source := videoReplayStore(t, "badger")
	records, job := correctionJob(t, source)
	intent := correctionIntent(t, job, "one")
	job, err := records.CreateCorrection(t.Context(), job, intent)
	require.NoError(t, err)
	job, err = records.ApplyCorrection(t.Context(), job, nil)
	require.NoError(t, err)
	job, err = records.MarkCorrectionReported(t.Context(), job, intent, "delivered")
	require.NoError(t, err)
	before := captureJobRecords(t, source)
	final, err := jobs.CaptureRecoveryJob(t.Context(), before, job.Account, job.ID)
	require.NoError(t, err)
	data, err := json.Marshal(final)
	require.NoError(t, err)
	assets, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	for _, field := range []string{"terminal_at", "native_retention", "native_receipt_key", "correction_reported", "correction_applied", "correction_head", "admin_decision", "accounted_at"} {
		t.Run(field, func(t *testing.T) {
			var changed map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(data, &changed))
			switch field {
			case "terminal_at":
				changed[field] = jsontext.Value(`"2099-01-01T00:00:00Z"`)
			case "native_retention":
				changed[field] = jsontext.Value(`36000000000000`)
			case "native_receipt_key":
				changed[field] = jsontext.Value(`"other"`)
			default:
				delete(changed, field)
			}
			raw, err := json.Marshal(changed)
			require.NoError(t, err)
			var next jobs.RecoveryJob
			err = json.Unmarshal(raw, &next)
			if err == nil {
				_, err = jobs.PrepareJobReplay(t.Context(), before, before, assets, time.Now(), next)
			}
			require.Error(t, err)
		})
	}
	mutation, err := jobs.PrepareJobReplay(t.Context(), before, before, assets, time.Now(), final)
	require.NoError(t, err)
	require.True(t, bytes.Equal(mutation.ExpectedValue, mutation.NewValue))
	changed, err := jobs.NewRecoveryJobCorrection(intent, true, "expired")
	require.NoError(t, err)
	_, err = jobs.PrepareJobCorrectionReplay(t.Context(), before, final, []jobs.RecoveryJobCorrection{changed})
	require.Error(t, err, "an immutable delivered report cannot become expired")
	intent.Decision.DecidedAt = intent.Decision.DecidedAt.Add(91 * 24 * time.Hour)
	expired, err := jobs.NewRecoveryJobCorrection(intent, false, "")
	require.NoError(t, err)
	_, err = jobs.PrepareJobCorrectionReplay(t.Context(), capturedJobRecords{}, final, []jobs.RecoveryJobCorrection{expired})
	require.Error(t, err, "restore must not restart the correction horizon")
}

func TestVideoJobReplayCorrectionIngressRefusesUnknownFacts(t *testing.T) {
	for _, data := range []string{`{"intent":{},"applied":false,"future_private_fact":1}`, `{"intent":{"future_private_fact":1},"applied":false}`, `{"intent":{},"applied":false,"applied":true}`} {
		var evidence jobs.RecoveryJobCorrection
		require.Error(t, json.Unmarshal([]byte(data), &evidence))
	}
}
