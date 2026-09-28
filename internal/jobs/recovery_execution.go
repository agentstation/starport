package jobs

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

// RecoveryExecution counts retained jobs with required accounting and pending settlement.
type RecoveryExecution struct {
	ReservedJobs      int64 `json:"reserved_jobs"`
	PendingSettlement int64 `json:"pending_settlement"`
}

// ReadRecoveryJob reads one captured job with its original storage expiry metadata.
func ReadRecoveryJob(ctx context.Context, source reservation.BackupReader, account, id string) (Job, error) {
	if source == nil {
		return Job{}, ErrCorruptRecord
	}
	key := storageKey(account, id)
	record, err := source.ReadCaptured(ctx, key, storage.TransferMaxValueBytes)
	if err != nil {
		return Job{}, err
	}
	job, err := decodeJob(record.Value)
	if err != nil || record.Key != key || record.ExpiresAtMillis != 0 || job.Account != account || job.ID != id {
		return Job{}, ErrCorruptRecord
	}
	return job, nil
}

// VerifyRecoveryExecution checks the pinned dispatch and preserves pending charge evidence.
// An absent reservation does not prove that this job needed no budget.
func VerifyRecoveryExecution(ctx context.Context, source reservation.BackupReader, job Job) (report RecoveryExecution, err error) {
	if source == nil || job.Validate() != nil {
		return report, ErrCorruptRecord
	}
	if job.ReservationID == "" {
		return report, nil
	}
	record, err := reservation.ReadBackupAttempt(ctx, source, job.ReservationID)
	if err != nil {
		return report, err
	}
	if err := verifyRecoveryDispatch(job, *record); err != nil {
		return report, err
	}
	report.ReservedJobs = 1
	evidence := job.BillingEvidence()
	if record.State == reservation.Reserved || record.State == reservation.Canceled {
		return report, ErrCorruptRecord
	}
	if record.State == reservation.Settled {
		if evidence != nil && !sameRecoveryEvidence(evidence, record.Evidence) {
			return report, ErrCorruptRecord
		}
	} else {
		report.PendingSettlement = 1
		if job.reportingComplete() {
			return report, ErrCorruptRecord
		}
		if evidence != nil && record.Pending != nil && !sameRecoveryEvidence(evidence, record.Pending) {
			return report, ErrCorruptRecord
		}
		if evidence != nil && record.Unresolved != nil && !sameRecoveryEvidence(evidence, record.Unresolved) {
			return report, ErrCorruptRecord
		}
	}
	if job.BillingConflict() && record.DisputeID == "" {
		return report, ErrCorruptRecord
	}
	if applied := job.AppliedCorrection(); applied != nil {
		if record.CorrectionID != applied.Decision.DecisionID {
			return report, ErrCorruptRecord
		}
		expected := recoveryBudgetCorrection(*applied)
		if err := reservation.VerifyBackupCorrectionBinding(ctx, source, job.ReservationID, expected); err != nil {
			return report, err
		}
	} else if record.CorrectionID != "" {
		return report, ErrCorruptRecord
	}
	return report, nil
}

func verifyRecoveryDispatch(job Job, record reservation.Record) error {
	attempt := record.Attempt
	if record.JobID != job.ID || !strings.HasPrefix(job.Model, job.Provider+"/") || attempt.AccountID != job.Account || attempt.KeyID != job.KeyID || attempt.OfferingID != job.Model || attempt.CatalogGeneration != job.CatalogGeneration || attempt.Operation != string(job.Operation) {
		return ErrCorruptRecord
	}
	if job.Valuation != nil && !reflect.DeepEqual(attempt.Valuation, *job.Valuation) {
		return ErrCorruptRecord
	}
	return nil
}

func sameRecoveryEvidence(a, b *reservation.Evidence) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.NoCharge == b.NoCharge && a.Tokens == b.Tokens && maps.Equal(a.Quantities, b.Quantities)
}

func recoveryBudgetCorrection(intent CorrectionIntent) reservation.Correction {
	return reservation.Correction{ID: intent.Decision.DecisionID, ExpectedBinding: intent.BudgetBinding, Actor: intent.Decision.Actor, EvidenceReference: intent.Decision.EvidenceReference, Reason: intent.Decision.Reason, Evidence: intent.Evidence()}
}

// VerifyRecoveryReservation checks a reverse job reference without repeating work.
// A missing job can follow interrupted creation or permitted deletion. The reservation stays in storage.
func VerifyRecoveryReservation(ctx context.Context, source reservation.BackupReader, record reservation.Record) (missing bool, err error) {
	if record.JobID == "" {
		return false, nil
	}
	job, err := ReadRecoveryJob(ctx, source, record.Attempt.AccountID, record.JobID)
	if errors.Is(err, storage.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if job.ReservationID != record.Attempt.ID {
		return false, ErrCorruptRecord
	}
	_, err = VerifyRecoveryExecution(ctx, source, job)
	return false, err
}
