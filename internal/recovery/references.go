package recovery

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/storage"
)

// ReferenceReport counts the domain checks actually performed on captured records.
// It does not establish later authorization, spending, or restoration permission.
type ReferenceReport struct {
	AccountRecords       int64                   `json:"account_records"`
	AccountTemplates     int64                   `json:"account_templates"`
	Identity             identity.RecoveryReport `json:"identity"`
	CredentialRecords    int64                   `json:"credential_records"`
	CredentialValues     int64                   `json:"credential_values"`
	FileRecords          int64                   `json:"file_records"`
	JobRecords           int64                   `json:"job_records"`
	UncertainJobs        int64                   `json:"uncertain_jobs"`
	BatchRecords         int64                   `json:"batch_records"`
	BatchLines           int64                   `json:"batch_lines"`
	UnfinishedBatchLines int64                   `json:"unfinished_batch_lines"`
	MissingBatchFiles    int64                   `json:"missing_batch_files"`
}

// InspectBundleReferences verifies a bundle, then checks private copies through domain owners.
// Scratch must be an existing private directory. Live stores and providers are never opened.
func InspectBundleReferences(ctx context.Context, directory, digest, scratch string, encryption *credentials.EncryptionService) (BundleManifest, ReferenceReport, error) {
	manifest, err := VerifyBundle(ctx, directory, digest, encryption)
	if err != nil {
		return BundleManifest{}, ReferenceReport{}, err
	}
	report, err := inspectVerifiedBundleReferences(ctx, directory, scratch, manifest, encryption)
	return manifest, report, err
}

func inspectVerifiedBundleReferences(ctx context.Context, directory, scratch string, manifest BundleManifest, encryption *credentials.EncryptionService) (report ReferenceReport, resultErr error) {
	records, err := OpenKVSnapshot(ctx, filepath.Join(directory, bundleKVFile), scratch, manifest.KV)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, records.Close()) }()
	if err := inspectIdentityReferences(ctx, directory, scratch, manifest, records, &report); err != nil {
		return report, err
	}
	blobs, err := blob.OpenSnapshot(ctx, filepath.Join(directory, bundleBlobFile), scratch, manifest.Blobs)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, blobs.Close()) }()
	err = records.Enumerate(ctx, func(record storage.TransferRecord) error {
		var checkErr error
		switch {
		case strings.HasPrefix(record.Key, account.StoragePrefix):
			_, checkErr = account.VerifyRecoveryRecord(record.Key, record.Value)
			if checkErr == nil {
				report.AccountRecords++
			}
		case strings.HasPrefix(record.Key, credentials.ProviderCredentialStoragePrefix):
			var count int
			count, checkErr = credentials.VerifyRecoveryRecord(ctx, record.Key, record.Value, encryption)
			if checkErr == nil {
				report.CredentialRecords++
				report.CredentialValues += int64(count)
			}
		case strings.HasPrefix(record.Key, files.StoragePrefix):
			_, checkErr = files.VerifyRecoveryRecord(ctx, record.Key, record.Value, blobs, manifest.StartedAt)
			if checkErr == nil {
				report.FileRecords++
			}
		case strings.HasPrefix(record.Key, jobs.BatchStoragePrefix):
			var missing int64
			_, missing, checkErr = jobs.VerifyRecoveryBatch(ctx, record.Key, record.Value, records)
			if checkErr == nil {
				report.BatchRecords++
				report.MissingBatchFiles += missing
			}
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
		case strings.HasPrefix(record.Key, jobs.StoragePrefix):
			var job jobs.Job
			job, checkErr = jobs.VerifyRecoveryRecord(ctx, record.Key, record.Value, blobs, manifest.StartedAt)
			if checkErr == nil {
				report.JobRecords++
				if job.SubmissionPending {
					report.UncertainJobs++
				}
			}
		}
		if checkErr != nil {
			return fmt.Errorf("recovery reference check failed for record %x: %w", sha256.Sum256([]byte(record.Key)), checkErr)
		}
		return nil
	})
	return report, err
}
