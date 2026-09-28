package recovery

import (
	"context"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
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

func inspectJobExecution(ctx context.Context, records *KVSnapshotView, blobs blob.SnapshotView, manifest BundleManifest, record storage.TransferRecord, report *ReferenceReport) error {
	var checkErr error
	var job jobs.Job
	job, checkErr = jobs.VerifyRecoveryRecord(ctx, record.Key, record.Value, blobs, manifest.StartedAt)
	if checkErr == nil && record.ExpiresAtMillis != 0 {
		checkErr = jobs.ErrCorruptRecord
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
