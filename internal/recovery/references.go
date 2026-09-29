package recovery

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// ReferenceReport counts verified records and retained recovery diagnostics.
// It does not authorize later spending or restore.
type ReferenceReport struct {
	JobSlots                      jobslots.RecoveryReport  `json:"job_slots"`
	JobExecution                  jobs.RecoveryExecution   `json:"job_execution"`
	JobCorrections                jobs.RecoveryCorrections `json:"job_corrections"`
	MissingReservationJobs        int64                    `json:"missing_reservation_jobs"`
	UnknownAccountBudgetHistories int64                    `json:"unknown_account_budget_histories"`
	BudgetWindows                 int64                    `json:"budget_windows"`
	BudgetRecords                 int64                    `json:"budget_records"`
	HeldReservations              int64                    `json:"held_reservations"`
	GatewayKeys                   apikey.RecoveryReport    `json:"gateway_keys"`
	AccountRecords                int64                    `json:"account_records"`
	AccountTemplates              int64                    `json:"account_templates"`
	Identity                      identity.RecoveryReport  `json:"identity"`
	CredentialRecords             int64                    `json:"credential_records"`
	CredentialValues              int64                    `json:"credential_values"`
	FileRecords                   int64                    `json:"file_records"`
	JobRecords                    int64                    `json:"job_records"`
	UncertainJobs                 int64                    `json:"uncertain_jobs"`
	BatchRecords                  int64                    `json:"batch_records"`
	BatchLines                    int64                    `json:"batch_lines"`
	UnfinishedBatchLines          int64                    `json:"unfinished_batch_lines"`
	MissingBatchFiles             int64                    `json:"missing_batch_files"`
}

// CapturedKVInspector checks domain records in a verified image at its closed recovery boundary.
// The view remains valid only during the call. Inspection grants no admission permission.
type CapturedKVInspector func(context.Context, *KVSnapshotView, Record) error

// InspectBundleReferences verifies a bundle, then checks private copies through domain owners.
// Scratch must be an existing private directory. Live stores and providers are never opened.
func InspectBundleReferences(ctx context.Context, directory, digest, scratch string, encryption *credentials.EncryptionService, inspectors ...CapturedKVInspector) (BundleManifest, ReferenceReport, error) {
	manifest, err := VerifyBundle(ctx, directory, digest, encryption)
	if err != nil {
		return BundleManifest{}, ReferenceReport{}, err
	}
	report, err := inspectVerifiedBundleReferences(ctx, directory, scratch, manifest, encryption, inspectors...)
	return manifest, report, err
}

func inspectVerifiedBundleReferences(ctx context.Context, directory, scratch string, manifest BundleManifest, encryption *credentials.EncryptionService, inspectors ...CapturedKVInspector) (report ReferenceReport, resultErr error) {
	records, err := OpenKVSnapshot(ctx, filepath.Join(directory, bundleKVFile), scratch, manifest.KV)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, records.Close()) }()
	image, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(directory, bundleSQLFile), manifest.SQL, scratch)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, image.Close()) }()
	blobs, err := blob.OpenSnapshot(ctx, filepath.Join(directory, bundleBlobFile), scratch, manifest.Blobs)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, blobs.Close()) }()
	return inspectCapturedReferences(ctx, records, image, blobs, manifest.Request.Boundary, manifest.StartedAt, encryption, inspectors...)
}

func inspectCapturedReferences(ctx context.Context, records *KVSnapshotView, image *sqlstore.RelationalSnapshotView, blobs blob.SnapshotView, boundary Record, capturedAt time.Time, encryption *credentials.EncryptionService, inspectors ...CapturedKVInspector) (report ReferenceReport, resultErr error) {
	if err := inspectCapturedIdentity(ctx, image, boundary, records, &report); err != nil {
		return report, err
	}
	accounting, err := openBackupAccountingIndex(ctx, records)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, accounting.Close()) }()
	slots, err := newBackupJobSlotIndex(ctx, accounting.tx)
	if err != nil {
		return report, err
	}
	correctionKinds := make(map[string]int64)
	err = records.Enumerate(ctx, func(record storage.TransferRecord) error {
		var checkErr error
		switch {
		case strings.HasPrefix(record.Key, jobslots.ReplayStoragePrefix):
			checkErr = jobslots.ErrHistoryUnknown
		case strings.HasPrefix(record.Key, reservation.WindowReplayPrefix):
			checkErr = reservation.ErrHistoryUnknown
		case strings.HasPrefix(record.Key, jobslots.ClaimStoragePrefix), strings.HasPrefix(record.Key, limits.OutstandingJobsPrefix):
			checkErr = slots.Add(ctx, record)
		case strings.HasPrefix(record.Key, reservation.StoragePrefix):
			checkErr = inspectBudgetExecution(ctx, records, accounting, record, &report)

		case strings.HasPrefix(record.Key, account.StoragePrefix):
			var owner account.Record
			owner, checkErr = verifyCapturedAccount(record)
			if checkErr == nil {
				var unknown int64
				unknown, checkErr = reservation.CheckBackupLimits(ctx, records, limits.ScopeAccount, owner.Account.ID, owner.Account.Limits)
				report.UnknownAccountBudgetHistories += unknown
			}
			if checkErr == nil {
				report.AccountRecords++
			}
		case strings.HasPrefix(record.Key, credentials.ProviderCredentialStoragePrefix):
			var count int
			count, checkErr = verifyCapturedCredential(ctx, record, encryption)
			if checkErr == nil {
				report.CredentialRecords++
				report.CredentialValues += int64(count)
			}
		case strings.HasPrefix(record.Key, files.StoragePrefix):
			_, checkErr = files.VerifyRecoveryRecord(ctx, record.Key, record.Value, blobs, capturedAt)
			if checkErr == nil {
				report.FileRecords++
			}
		case strings.HasPrefix(record.Key, jobs.BatchStoragePrefix):
			checkErr = inspectBatchExecution(ctx, records, record, &report, slots)

		case strings.HasPrefix(record.Key, jobs.BatchLineStoragePrefix):
			var missing bool
			var line jobs.BatchLine
			line, missing, checkErr = jobs.VerifyRecoveryBatchLine(ctx, record.Key, record.Value, records)
			if checkErr == nil {
				report.BatchLines++
				if !line.ResultReady {
					report.UnfinishedBatchLines++
				}
				if missing {
					report.MissingBatchFiles++
				}
			}
		case strings.HasPrefix(record.Key, jobs.CorrectionStoragePrefix):
			var kind string
			kind, checkErr = jobs.VerifyRecoveryCorrectionRecord(ctx, records, record)
			if checkErr == nil {
				correctionKinds[kind]++
			}
		case strings.HasPrefix(record.Key, jobs.StoragePrefix):
			checkErr = inspectJobExecution(ctx, records, blobs, capturedAt, record, &report, slots)
		}
		if checkErr != nil {
			return fmt.Errorf("recovery reference check failed for record %x: %w", sha256.Sum256([]byte(record.Key)), checkErr)
		}
		return nil
	})
	if err == nil {
		err = verifyCorrectionCounts(correctionKinds, report)
	}
	if err == nil {
		report.BudgetWindows, err = accounting.Verify(ctx)
	}
	if err == nil {
		report.JobSlots, err = slots.Verify(ctx)
	}
	if err == nil {
		err = inspectCapturedDomains(ctx, records, boundary, inspectors)
	}
	return report, err
}

func inspectCapturedDomains(ctx context.Context, records *KVSnapshotView, boundary Record, inspectors []CapturedKVInspector) error {
	for _, inspect := range inspectors {
		if inspect == nil {
			return errors.New("backup record inspector is required")
		}
		if err := inspect(ctx, records, boundary); err != nil {
			return err
		}
	}
	return nil
}

func verifyCorrectionCounts(correctionKinds map[string]int64, report ReferenceReport) error {
	if correctionKinds["intent"] != report.JobCorrections.Intents || correctionKinds["history-next"] != report.JobCorrections.Intents || correctionKinds["applied"] != report.JobCorrections.Applied || correctionKinds["applied-next"] != report.JobCorrections.Applied || correctionKinds["reported"] != report.JobCorrections.Reported {
		return jobs.ErrCorruptRecord
	}
	return nil
}

func verifyCapturedAccount(record storage.TransferRecord) (account.Record, error) {
	var strict account.RecoveryRecord
	if record.ExpiresAtMillis != 0 {
		return account.Record{}, account.ErrCorruptRecord
	}
	if err := strict.UnmarshalJSON(record.Value); err != nil {
		return account.Record{}, err
	}
	return account.VerifyRecoveryRecord(record.Key, record.Value)
}

func verifyCapturedCredential(ctx context.Context, record storage.TransferRecord, encryption *credentials.EncryptionService) (int, error) {
	var strict credentials.RecoveryRecord
	if record.ExpiresAtMillis != 0 {
		return 0, credentials.ErrCorruptRecord
	}
	if err := strict.UnmarshalJSON(record.Value); err != nil {
		return 0, err
	}
	return credentials.VerifyRecoveryRecord(ctx, record.Key, record.Value, encryption)
}
