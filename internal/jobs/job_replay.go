package jobs

import (
	"context"
	legacyjson "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

// RecoveryJob contains private operator recovery evidence, including provider identifiers.
// Its JSON representation is the complete durable record. It must not reach public APIs or logs.
type RecoveryJob struct{ *recoveryJobState }

type recoveryJobState struct{ job Job }

func (RecoveryJob) String() string { return "<private job recovery evidence>" }

// GoString prevents formatted diagnostics from exposing private recovery evidence.
func (RecoveryJob) GoString() string { return "<private job recovery evidence>" }

// Format excludes private job evidence from diagnostic formatting.
func (RecoveryJob) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private job recovery evidence>"))
}

// MarshalJSON preserves private fields absent from the public Job representation.
func (r RecoveryJob) MarshalJSON() ([]byte, error) {
	if r.recoveryJobState == nil {
		return nil, ErrCorruptRecord
	}
	return encodeJob(r.job)
}

// UnmarshalJSON checks the durable schema before accepting private recovery evidence.
func (r *RecoveryJob) UnmarshalJSON(data []byte) error {
	if r == nil || len(data) > storage.TransferMaxValueBytes {
		return ErrCorruptRecord
	}
	var schema jobRecord
	if err := json.Unmarshal(data, &schema, json.RejectUnknownMembers(true), legacyjson.FormatDurationAsNano(true)); err != nil {
		return ErrCorruptRecord
	}
	job, err := decodeJob(data)
	if err != nil {
		return err
	}
	r.recoveryJobState = &recoveryJobState{job: job}
	return nil
}

// CaptureRecoveryJob copies a complete job from independently retained records.
func CaptureRecoveryJob(ctx context.Context, source reservation.BackupReader, account, id string) (RecoveryJob, error) {
	if ctx == nil || source == nil {
		return RecoveryJob{}, ErrCorruptRecord
	}
	key := storageKey(account, id)
	record, err := source.ReadCaptured(ctx, key, storage.TransferMaxValueBytes)
	if err != nil {
		return RecoveryJob{}, err
	}
	if record.Key != key || record.ExpiresAtMillis != 0 {
		return RecoveryJob{}, ErrCorruptRecord
	}
	var result RecoveryJob
	err = result.UnmarshalJSON(record.Value)
	if err == nil && (result.job.Account != account || result.job.ID != id) {
		err = ErrCorruptRecord
	}
	return result, err
}

// PrepareJobReplay prepares the final job write without dispatching or changing storage.
// The before snapshot must remain immutable for every exact retry.
// The after snapshot includes staged corrections and proposed accounting changes under closed authority.
// The coordinator must combine the returned write with related claims and accounting writes.
func PrepareJobReplay(ctx context.Context, before, after reservation.BackupReader, assets blob.PublicationReader, capturedAt time.Time, next RecoveryJob) (storage.CompareAndSwapMutation, error) {
	if ctx == nil || before == nil || after == nil || assets == nil || capturedAt.IsZero() || next.recoveryJobState == nil {
		return storage.CompareAndSwapMutation{}, ErrCorruptRecord
	}
	if err := ctx.Err(); err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	job := next.job
	data, err := encodeJob(job)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	key := storageKey(job.Account, job.ID)
	if _, err := VerifyRecoveryRecord(ctx, key, data, assets, capturedAt); err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if _, err := VerifyRecoveryExecution(ctx, after, job); err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if _, err := VerifyRecoveryCorrections(ctx, after, job); err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	original, err := before.ReadCaptured(ctx, key, storage.TransferMaxValueBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.CompareAndSwapMutation{Key: key, NewValue: data}, nil
	}
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	var prior RecoveryJob
	if err := prior.UnmarshalJSON(original.Value); err != nil || original.Key != key || original.ExpiresAtMillis != 0 {
		return storage.CompareAndSwapMutation{}, ErrCorruptRecord
	}
	old := prior.job
	if err := verifyJobReplayTransition(old, job); err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if err := verifyJobReplayAncestors(ctx, after, old, job); err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	return storage.CompareAndSwapMutation{Key: key, ExpectedValue: original.Value, NewValue: data}, nil
}

func verifyJobReplayTransition(old, next Job) error {
	if !sameJobIdentity(old, next) || old.Native != next.Native || old.SlotID != next.SlotID || old.CatalogGeneration != next.CatalogGeneration || old.ReservationID != next.ReservationID || old.nativeAssetBound != next.nativeAssetBound || old.nativeRetention != next.nativeRetention || old.nativeReceiptKey != next.nativeReceiptKey || old.nativeAssetKey != next.nativeAssetKey || !reflect.DeepEqual(old.Valuation, next.Valuation) {
		return ErrInvalidJob
	}
	if old.State != next.State && !CanTransition(old.State, next.State) {
		return ErrIllegalTransition
	}
	if old.providerJobID != "" && old.providerJobID != next.providerJobID {
		return ErrInvalidJob
	}
	if !old.SubmissionPending && next.SubmissionPending || old.SlotReleased && !next.SlotReleased {
		return ErrInvalidJob
	}
	if old.Measurement != nil && !reflect.DeepEqual(old.Measurement, next.Measurement) {
		return ErrInvalidJob
	}
	if err := validateRetainedJobEvidence(old, next); err != nil {
		return err
	}
	return verifyJobReplayRetainedOutcome(old, next)
}

func verifyJobReplayRetainedOutcome(old, next Job) error {
	if old.nativeAssetDigest != "" && (old.nativeAssetDigest != next.nativeAssetDigest || old.nativeAssetContentType != next.nativeAssetContentType) {
		return ErrInvalidJob
	}
	if old.assetRecoveryStatus == assetRecoveryExpired && next.assetRecoveryStatus != assetRecoveryExpired {
		return ErrInvalidJob
	}
	if old.AssetKey != "" && (old.AssetKey != next.AssetKey || old.AssetBytes != next.AssetBytes || old.AssetContentType != next.AssetContentType || !old.AssetExpiresAt.Equal(next.AssetExpiresAt)) {
		return ErrInvalidJob
	}
	for _, pair := range [][2]time.Time{{old.TerminalAt, next.TerminalAt}, {old.AccountedAt, next.AccountedAt}, {old.ReportingExpiredAt, next.ReportingExpiredAt}, {old.NotificationAttemptedAt, next.NotificationAttemptedAt}} {
		if !pair[0].IsZero() && !pair[0].Equal(pair[1]) {
			return ErrInvalidJob
		}
	}
	if old.State.Terminal() && old.Reason != next.Reason {
		return ErrInvalidJob
	}
	return nil
}

func verifyJobReplayAncestors(ctx context.Context, source reservation.BackupReader, old, next Job) error {
	heads := [][2]string{{correctionID(old.correctionHead), correctionID(next.correctionHead)}, {correctionID(old.correctionApplied), correctionID(next.correctionApplied)}, {old.correctionReported, next.correctionReported}}
	for i, pair := range heads {
		if pair[0] == "" {
			continue
		}
		id := pair[1]
		for id != "" && id != pair[0] {
			intent, err := recoveryCorrectionIntent(ctx, source, next, id)
			if err != nil {
				return err
			}
			id = intent.PreviousAppliedID
			if i == 0 {
				id = intent.PreviousID
			}
		}
		if id != pair[0] {
			return ErrInvalidJob
		}
	}
	for _, prior := range []*CorrectionIntent{old.correctionHead, old.correctionApplied} {
		if prior == nil {
			continue
		}
		retained, err := recoveryCorrectionIntent(ctx, source, next, prior.Decision.DecisionID)
		if err != nil {
			return err
		}
		if !sameCorrectionIntent(*prior, retained) {
			return ErrInvalidJob
		}
	}
	return nil
}
