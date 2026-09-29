package recovery

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBackupJobSlotsRejectLostAttachment(t *testing.T) {
	for _, lostSlot := range []bool{false, true} {
		t.Run(fmt.Sprint(lostSlot), func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			meter, err := jobslots.Open(kv)
			require.NoError(t, err)
			job, err := jobs.New("job", "owner", "provider", "model", routing.OperationVideosGenerations, time.Now())
			require.NoError(t, err)
			job.SlotID = "slot"
			require.NoError(t, meter.Reserve(t.Context(), job.Account, job.SlotID, job.ID, "video", 1))
			attachment, err := meter.Attachment(t.Context(), job.Account, job.SlotID, job.ID, "video")
			require.NoError(t, err)
			records, err := jobs.OpenRepository(kv)
			require.NoError(t, err)
			require.NoError(t, records.CreateClaimed(t.Context(), job, attachment))
			var claim jobslots.Claim
			require.NoError(t, json.Unmarshal(attachment.NewValue, &claim))
			claim.Attached = false
			data, err := json.Marshal(claim)
			require.NoError(t, err)
			require.NoError(t, kv.Set(t.Context(), attachment.Key, data))
			if lostSlot {
				keys, err := kv.ScanWithPrefix(t.Context(), jobs.StoragePrefix, 0)
				require.NoError(t, err)
				require.Len(t, keys, 1)
				data, err := kv.Get(t.Context(), keys[0])
				require.NoError(t, err)
				var fields map[string]any
				require.NoError(t, json.Unmarshal(data, &fields))
				delete(fields, "slot_id")
				data, err = json.Marshal(fields)
				require.NoError(t, err)
				require.NoError(t, kv.Set(t.Context(), keys[0], data))
			}
			_, err = inspectReferenceFixture(t, source, request, destination)
			require.ErrorIs(t, err, jobslots.ErrHistoryUnknown, "lost attachment must not qualify a claim for pending cleanup")
		})
	}
}

func TestBackupJobSlotOwnership(t *testing.T) {
	for _, backend := range []string{storage.StorageTypeBadger, storage.StorageTypeValkey} {
		t.Run(backend, func(t *testing.T) {
			for _, mode := range []string{"active", "pending", "released-without-job", "release-ack-pending", "missing-counter", "missing-history", "low-counter", "high-counter", "missing-claim", "missing-job", "wrong-job", "wrong-kind", "released-active", "expiring-claim"} {
				t.Run(mode, func(t *testing.T) {
					source, request, destination := backupBundleFixture(t)
					kv, transfer, _ := kvTransferStores(t, backend)
					source.KV = transfer
					meter, err := jobslots.Open(kv)
					require.NoError(t, err)
					job, err := jobs.New("job", "owner", "provider", "model", routing.OperationVideosGenerations, time.Now())
					require.NoError(t, err)
					job.SlotID = "slot"
					require.NoError(t, meter.Reserve(t.Context(), job.Account, job.SlotID, job.ID, "video", 1))
					attachment, err := meter.Attachment(t.Context(), job.Account, job.SlotID, job.ID, "video")
					require.NoError(t, err)
					if mode == "release-ack-pending" {
						require.NoError(t, job.Fail("provider-confirmed", time.Now()))
					}
					if mode != "pending" && mode != "released-without-job" {
						records, err := jobs.OpenRepository(kv)
						require.NoError(t, err)
						require.NoError(t, records.CreateClaimed(t.Context(), job, attachment))
						if mode == "missing-job" {
							require.NoError(t, records.Delete(t.Context(), job.Account, job.ID))
						}
					}
					switch mode {
					case "released-without-job", "release-ack-pending", "released-active":
						require.NoError(t, meter.Release(t.Context(), job.Account, job.SlotID))
					case "missing-counter":
						require.NoError(t, kv.Delete(t.Context(), limits.OutstandingJobsPrefix+job.Account))
					case "missing-history":
						keys, err := kv.ScanWithPrefix(t.Context(), jobslots.ClaimStoragePrefix, 0)
						require.NoError(t, err)
						for _, key := range keys {
							if strings.HasSuffix(key, ":history") {
								require.NoError(t, kv.Delete(t.Context(), key))
							}
						}
					case "low-counter", "high-counter":
						total := 0
						if mode == "high-counter" {
							total = 2
						}
						require.NoError(t, kv.Set(t.Context(), limits.OutstandingJobsPrefix+job.Account, []byte(fmt.Sprintf(`{"version":3,"total":%d}`, total))))
					case "missing-claim":
						require.NoError(t, kv.Delete(t.Context(), attachment.Key))
					case "wrong-job", "wrong-kind":
						var claim jobslots.Claim
						require.NoError(t, json.Unmarshal(attachment.NewValue, &claim))
						if mode == "wrong-job" {
							claim.JobID = "another"
						} else {
							claim.Kind = "batch"
						}
						data, err := json.Marshal(claim)
						require.NoError(t, err)
						require.NoError(t, kv.Set(t.Context(), attachment.Key, data))
					case "expiring-claim":
						require.NoError(t, kv.ExpireAt(t.Context(), attachment.Key, time.Now().Add(time.Hour)))
					}
					if mode == "pending" {
						claim, err := meter.Get(t.Context(), job.Account, job.SlotID)
						require.NoError(t, err)
						claim.CreatedAt = time.Now().Add(-7 * 24 * time.Hour)
						data, err := json.Marshal(claim)
						require.NoError(t, err)
						require.NoError(t, kv.Set(t.Context(), attachment.Key, data))
					}
					before, beforeErr := kv.Get(t.Context(), attachment.Key)
					report, err := inspectReferenceFixture(t, source, request, destination)
					after, afterErr := kv.Get(t.Context(), attachment.Key)
					require.Equal(t, before, after, "verification must not release or repair a claim")
					require.Equal(t, beforeErr, afterErr)
					switch mode {
					case "active", "pending", "released-without-job", "release-ack-pending":
						require.NoError(t, err)
						require.EqualValues(t, 1, report.JobSlots.Accounts)
						require.EqualValues(t, 1, report.JobSlots.Claims)
						if mode == "active" || mode == "pending" {
							require.EqualValues(t, 1, report.JobSlots.Held)
						} else {
							require.Zero(t, report.JobSlots.Held)
						}
						if mode == "pending" {
							require.EqualValues(t, 1, report.JobSlots.Pending)
						}
					default:
						require.ErrorIs(t, err, jobslots.ErrHistoryUnknown)
					}
				})
			}
		})
	}
}

func TestBackupBatchSlotReleaseRequiresFinishedRun(t *testing.T) {
	for _, mode := range []string{"active", "terminal-with-active-lines", "finished", "missing-claim"} {
		t.Run(mode, func(t *testing.T) {
			source, request, destination := backupBundleFixture(t)
			kv, transfer, _ := kvTransferStores(t, storage.StorageTypeBadger)
			source.KV = transfer
			meter, err := jobslots.Open(kv)
			require.NoError(t, err)
			batch, err := jobs.NewBatch("batch", "owner", "/v1/chat/completions", "deleted-input", time.Now())
			require.NoError(t, err)
			batch.SlotID = "slot"
			require.NoError(t, meter.Reserve(t.Context(), batch.Account, batch.SlotID, batch.ID, "batch", 1))
			attachment, err := meter.Attachment(t.Context(), batch.Account, batch.SlotID, batch.ID, "batch")
			require.NoError(t, err)
			if mode == "terminal-with-active-lines" || mode == "finished" {
				require.NoError(t, batch.Fail("interrupted", time.Now()))
				batch.RunFinished = mode == "finished"
			}
			records, err := jobs.OpenBatchRepository(kv)
			require.NoError(t, err)
			require.NoError(t, records.CreateClaimed(t.Context(), batch, attachment))
			if mode == "terminal-with-active-lines" || mode == "finished" {
				require.NoError(t, meter.Release(t.Context(), batch.Account, batch.SlotID))
			}
			if mode == "missing-claim" {
				require.NoError(t, kv.Delete(t.Context(), attachment.Key))
			}
			report, err := inspectReferenceFixture(t, source, request, destination)
			if mode == "active" || mode == "finished" {
				require.NoError(t, err)
				require.EqualValues(t, 1, report.JobSlots.Attachments)
			} else {
				require.ErrorIs(t, err, jobslots.ErrHistoryUnknown)
			}
		})
	}
}
