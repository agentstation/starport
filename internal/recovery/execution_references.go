package recovery

import (
	"context"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

func inspectBudgetExecution(ctx context.Context, records *KVSnapshotView, accounting *backupAccountingIndex, record storage.TransferRecord, report *ReferenceReport) error {
	var checkErr error
	var budget reservation.BackupRecord
	budget, checkErr = reservation.VerifyBackupRecord(ctx, records, record)
	if checkErr == nil {
		checkErr = accounting.Add(ctx, record.Key, budget)
	}
	if checkErr == nil && budget.Attempt != nil {
		var missing bool
		missing, checkErr = jobs.VerifyRecoveryReservation(ctx, records, *budget.Attempt)
		if missing {
			report.MissingReservationJobs++
		}
	}
	if checkErr == nil {
		report.BudgetRecords++
		if budget.Held {
			report.HeldReservations++
		}
	}
	return checkErr
}

func inspectJobExecution(ctx context.Context, records *KVSnapshotView, blobs blob.SnapshotView, manifest BundleManifest, record storage.TransferRecord, report *ReferenceReport, slots *backupJobSlotIndex) error {
	var checkErr error
	var job jobs.Job
	job, checkErr = jobs.VerifyRecoveryRecord(ctx, record.Key, record.Value, blobs, manifest.StartedAt)
	if checkErr == nil && record.ExpiresAtMillis != 0 {
		checkErr = jobs.ErrCorruptRecord
	}
	if checkErr == nil {
		checkErr = slots.Attach(ctx, jobslots.RecoveryAttachment{Account: job.Account, ClaimID: job.SlotID, JobID: job.ID, Kind: "video", Released: job.SlotReleased, Finished: job.State.Terminal()})
	}
	if checkErr == nil {
		var execution jobs.RecoveryExecution
		execution, checkErr = jobs.VerifyRecoveryExecution(ctx, records, job)
		report.JobExecution.ReservedJobs += execution.ReservedJobs
		report.JobExecution.PendingSettlement += execution.PendingSettlement
	}
	if checkErr == nil {
		var corrections jobs.RecoveryCorrections
		corrections, checkErr = jobs.VerifyRecoveryCorrections(ctx, records, job)
		report.JobCorrections.Intents += corrections.Intents
		report.JobCorrections.Applied += corrections.Applied
		report.JobCorrections.Reported += corrections.Reported
	}
	if checkErr == nil {
		report.JobRecords++
		if job.SubmissionPending {
			report.UncertainJobs++
		}
	}
	return checkErr
}

func inspectBatchExecution(ctx context.Context, records *KVSnapshotView, record storage.TransferRecord, report *ReferenceReport, slots *backupJobSlotIndex) error {
	batch, missing, err := jobs.VerifyRecoveryBatch(ctx, record.Key, record.Value, records)
	if err != nil {
		return err
	}
	if record.ExpiresAtMillis != 0 {
		return jobs.ErrCorruptBatchRecord
	}
	if err := slots.Attach(ctx, jobslots.RecoveryAttachment{Account: batch.Account, ClaimID: batch.SlotID, JobID: batch.ID, Kind: "batch", Released: batch.SlotReleased, Finished: batch.RunFinished}); err != nil {
		return err
	}
	report.BatchRecords++
	report.MissingBatchFiles += missing
	return nil
}
