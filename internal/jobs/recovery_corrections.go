package jobs

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

// CorrectionStoragePrefix identifies immutable job decisions and outcome records.
const CorrectionStoragePrefix = correctionPrefix

// RecoveryCorrections counts the records reached from retained job audit heads.
type RecoveryCorrections struct {
	Intents  int64 `json:"intents"`
	Applied  int64 `json:"applied"`
	Reported int64 `json:"reported"`
}

func readRecoveryCorrection(ctx context.Context, source reservation.BackupReader, job Job, kind, id string) ([]byte, error) {
	key := correctionKey(job.Account, job.ID, kind, id)
	record, err := source.ReadCaptured(ctx, key, maxCorrectionRecordBytes)
	if err != nil {
		return nil, err
	}
	if record.Key != key || record.ExpiresAtMillis != 0 || len(record.Value) == 0 {
		return nil, ErrCorruptRecord
	}
	return record.Value, nil
}

func recoveryCorrectionIntent(ctx context.Context, source reservation.BackupReader, job Job, id string) (CorrectionIntent, error) {
	data, err := readRecoveryCorrection(ctx, source, job, "intent", id)
	if err != nil {
		return CorrectionIntent{}, err
	}
	return decodeCorrection(data, job, id)
}

func verifyRecoveryCorrectionLink(ctx context.Context, source reservation.BackupReader, job Job, kind, previous, next string) error {
	data, err := readRecoveryCorrection(ctx, source, job, kind, previous)
	if err != nil {
		return err
	}
	actual, err := decodeCorrectionEvent(data, job, previous)
	if err != nil || actual != next {
		return ErrCorruptRecord
	}
	return nil
}

// correctionRecoveryWalk detects repeated ancestors without retaining the entire history.
type correctionRecoveryWalk struct {
	anchor      string
	span, power int64
}

func (w *correctionRecoveryWalk) advance(id string) error {
	if w.power == 0 {
		w.anchor, w.power = id, 1
		return nil
	}
	w.span++
	if id != "" && id == w.anchor {
		return ErrCorruptRecord
	}
	if w.span == w.power {
		if w.power > math.MaxInt64/2 {
			return ErrCorruptRecord
		}
		w.anchor, w.span, w.power = id, 0, w.power*2
	}
	return nil
}

// VerifyRecoveryCorrections checks both immutable decision chains and the report cursor.
// Pending intent stays pending. Inspection never applies a correction or reports usage.
func VerifyRecoveryCorrections(ctx context.Context, source reservation.BackupReader, job Job) (report RecoveryCorrections, resultErr error) {
	if source == nil || job.Validate() != nil {
		return report, ErrCorruptRecord
	}
	id := correctionID(job.correctionHead)
	var walk correctionRecoveryWalk
	var later time.Time
	var expectedApplied int64
	expectedAppliedID := correctionID(job.correctionApplied)
	for id != "" {
		if err := walk.advance(id); err != nil {
			return report, err
		}
		intent, err := recoveryCorrectionIntent(ctx, source, job, id)
		if err != nil {
			return report, err
		}
		if id == correctionID(job.correctionHead) && !sameCorrectionIntent(intent, *job.correctionHead) {
			return report, ErrCorruptRecord
		}
		if !later.IsZero() && intent.Decision.DecidedAt.After(later) {
			return report, ErrCorruptRecord
		}
		if err := verifyRecoveryCorrectionLink(ctx, source, job, "history-next", intent.PreviousID, id); err != nil {
			return report, err
		}
		applied, err := readRecoveryCorrection(ctx, source, job, "applied", id)
		if err == nil {
			encoded, err := encodeCorrection(intent)
			if err != nil || !bytes.Equal(applied, encoded) {
				return report, ErrCorruptRecord
			}
			if id != expectedAppliedID {
				return report, ErrCorruptRecord
			}
			expectedAppliedID = intent.PreviousAppliedID
			expectedApplied++
		} else if !errors.Is(err, storage.ErrNotFound) {
			return report, err
		} else if intent.PreviousAppliedID != expectedAppliedID {
			return report, ErrCorruptRecord
		}
		report.Intents++
		later, id = intent.Decision.DecidedAt, intent.PreviousID
	}
	if expectedAppliedID != "" {
		return report, ErrCorruptRecord
	}
	report.Applied, report.Reported, resultErr = verifyRecoveryAppliedCorrections(ctx, source, job)
	if resultErr == nil && report.Applied != expectedApplied {
		resultErr = ErrCorruptRecord
	}
	return report, resultErr
}

func verifyRecoveryAppliedCorrections(ctx context.Context, source reservation.BackupReader, job Job) (applied, reported int64, resultErr error) {
	id := correctionID(job.correctionApplied)
	var walk correctionRecoveryWalk
	reachedCursor := false
	for id != "" {
		if err := walk.advance(id); err != nil {
			return applied, reported, err
		}
		intent, err := recoveryCorrectionIntent(ctx, source, job, id)
		if err != nil {
			return applied, reported, err
		}
		if id == correctionID(job.correctionApplied) && !sameCorrectionIntent(intent, *job.correctionApplied) {
			return applied, reported, ErrCorruptRecord
		}
		data, err := readRecoveryCorrection(ctx, source, job, "applied", id)
		if err != nil {
			return applied, reported, err
		}
		encoded, err := encodeCorrection(intent)
		if err != nil || !bytes.Equal(data, encoded) {
			return applied, reported, ErrCorruptRecord
		}
		if err := verifyRecoveryCorrectionLink(ctx, source, job, "applied-next", intent.PreviousAppliedID, id); err != nil {
			return applied, reported, err
		}
		if job.ReservationID != "" {
			if err := reservation.VerifyBackupCorrectionBinding(ctx, source, job.ReservationID, recoveryBudgetCorrection(intent)); err != nil {
				return applied, reported, err
			}
		}
		reachedCursor = reachedCursor || id == job.correctionReported
		if err := verifyRecoveryReport(ctx, source, job, id, reachedCursor); err != nil {
			return applied, reported, err
		}
		if reachedCursor {
			reported++
		}
		applied++
		id = intent.PreviousAppliedID
	}
	if job.correctionReported != "" && !reachedCursor {
		return applied, reported, ErrCorruptRecord
	}
	return applied, reported, nil
}

func verifyRecoveryReport(ctx context.Context, source reservation.BackupReader, job Job, id string, required bool) error {
	data, err := readRecoveryCorrection(ctx, source, job, "reported", id)
	if !required && errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !required {
		return ErrCorruptRecord
	}
	status, err := decodeCorrectionEvent(data, job, id)
	if err != nil || (status != correctionReportDelivered && status != correctionReportExpired && status != correctionReportDisabled) {
		return ErrCorruptRecord
	}
	return nil
}

// VerifyRecoveryCorrectionRecord validates a retained audit record's storage identity.
// The coordinator compares namespace counts with the records reached from job heads.
func VerifyRecoveryCorrectionRecord(ctx context.Context, source reservation.BackupReader, record storage.TransferRecord) (kind string, resultErr error) {
	parts := strings.Split(strings.TrimPrefix(record.Key, correctionPrefix), ":")
	if !strings.HasPrefix(record.Key, correctionPrefix) || len(parts) != 5 || parts[1] != "job" || record.ExpiresAtMillis != 0 {
		return "", ErrCorruptRecord
	}
	account, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrCorruptRecord
	}
	jobID, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", ErrCorruptRecord
	}
	id, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil {
		return "", ErrCorruptRecord
	}
	kind = parts[3]
	if record.Key != correctionKey(string(account), string(jobID), kind, string(id)) {
		return "", ErrCorruptRecord
	}
	job, err := ReadRecoveryJob(ctx, source, string(account), string(jobID))
	if err != nil {
		return "", err
	}
	switch kind {
	case "intent", "applied":
		_, err = decodeCorrection(record.Value, job, string(id))
	case "history-next", "applied-next", "reported":
		_, err = decodeCorrectionEvent(record.Value, job, string(id))
	default:
		err = ErrCorruptRecord
	}
	return kind, err
}
