package jobs

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

// RecoveryJobCorrection retains one private correction and its immutable outcomes.
// Supersession follows Intent.PreviousID. ReportStatus is empty until reporting ends.
type RecoveryJobCorrection struct{ state *recoveryCorrectionState }

type recoveryCorrectionState struct {
	Intent       CorrectionIntent `json:"intent"`
	Applied      bool             `json:"applied"`
	ReportStatus string           `json:"report_status,omitempty"`
}

func (RecoveryJobCorrection) String() string { return "<private job correction evidence>" }

// GoString prevents formatted diagnostics from exposing private correction evidence.
func (RecoveryJobCorrection) GoString() string { return "<private job correction evidence>" }

// Format excludes private correction evidence from diagnostic formatting.
func (RecoveryJobCorrection) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private job correction evidence>"))
}

// MarshalJSON returns complete private correction evidence with its existing durable schema.
func (r RecoveryJobCorrection) MarshalJSON() ([]byte, error) {
	if r.state == nil {
		return nil, ErrCorruptRecord
	}
	return json.Marshal(r.state, json.Deterministic(true))
}

// NewRecoveryJobCorrection copies private evidence without exposing mutable retained fields.
// PrepareJobCorrectionReplay validates the correction against its final job.
func NewRecoveryJobCorrection(intent CorrectionIntent, applied bool, reportStatus string) (RecoveryJobCorrection, error) {
	data, err := json.Marshal(recoveryCorrectionState{Intent: intent, Applied: applied, ReportStatus: reportStatus}, json.Deterministic(true))
	if err != nil {
		return RecoveryJobCorrection{}, ErrCorruptRecord
	}
	var result RecoveryJobCorrection
	err = result.UnmarshalJSON(data)
	return result, err
}

// UnmarshalJSON rejects unsupported private correction facts.
func (r *RecoveryJobCorrection) UnmarshalJSON(data []byte) error {
	if r == nil || len(data) > maxCorrectionRecordBytes {
		return ErrCorruptRecord
	}
	var result recoveryCorrectionState
	if err := json.Unmarshal(data, &result, json.RejectUnknownMembers(true)); err != nil {
		return ErrCorruptRecord
	}
	r.state = &result
	return nil
}

// PrepareJobCorrectionReplay stages at most sixteen complete correction outcomes.
// A longer history uses multiple ordered steps while startup and dispatch remain closed.
// Final job publication must validate the full chain with PrepareJobReplay.
// source is the immutable pre-step view, including any previously staged steps.
func PrepareJobCorrectionReplay(ctx context.Context, source reservation.BackupReader, final RecoveryJob, corrections []RecoveryJobCorrection) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || len(corrections) == 0 || len(corrections) > 16 || final.recoveryJobState == nil || final.job.Validate() != nil {
		return nil, ErrInvalidJob
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	changes := map[string]storage.CompareAndSwapMutation{}
	for _, correction := range corrections {
		mutations, err := jobCorrectionReplayRecords(final.job, correction)
		if err != nil {
			return nil, err
		}
		for _, mutation := range mutations {
			if _, found := changes[mutation.Key]; found {
				return nil, ErrInvalidJob
			}
			prior, err := source.ReadCaptured(ctx, mutation.Key, maxCorrectionRecordBytes)
			if err == nil {
				if prior.Key != mutation.Key || prior.ExpiresAtMillis != 0 || !bytes.Equal(prior.Value, mutation.NewValue) {
					return nil, ErrCorruptRecord
				}
				mutation.ExpectedValue = bytes.Clone(prior.Value)
			} else if !errors.Is(err, storage.ErrNotFound) {
				return nil, err
			}
			changes[mutation.Key] = mutation
		}
	}
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]storage.CompareAndSwapMutation, 0, len(keys))
	for _, key := range keys {
		result = append(result, changes[key])
	}
	return result, nil
}

func jobCorrectionReplayRecords(job Job, correction RecoveryJobCorrection) ([]storage.CompareAndSwapMutation, error) {
	if correction.state == nil {
		return nil, ErrCorruptRecord
	}
	intent := correction.state.Intent
	if !intent.validFor(job) || correction.state.ReportStatus != "" && (!correction.state.Applied || correction.state.ReportStatus != correctionReportDelivered && correction.state.ReportStatus != correctionReportExpired && correction.state.ReportStatus != correctionReportDisabled) {
		return nil, ErrInvalidJob
	}
	data, err := encodeCorrection(intent)
	if err != nil {
		return nil, err
	}
	result := []storage.CompareAndSwapMutation{{Key: correctionKey(job.Account, job.ID, "intent", intent.Decision.DecisionID), NewValue: data}}
	link, err := eventMutation(job, "history-next", intent.PreviousID, intent.Decision.DecisionID)
	if err != nil {
		return nil, err
	}
	result = append(result, link)
	if correction.state.Applied {
		result = append(result, storage.CompareAndSwapMutation{Key: correctionKey(job.Account, job.ID, "applied", intent.Decision.DecisionID), NewValue: bytes.Clone(data)})
		link, err = eventMutation(job, "applied-next", intent.PreviousAppliedID, intent.Decision.DecisionID)
		if err != nil {
			return nil, err
		}
		result = append(result, link)
	}
	if correction.state.ReportStatus != "" {
		link, err = eventMutation(job, "reported", intent.Decision.DecisionID, correction.state.ReportStatus)
		if err != nil {
			return nil, err
		}
		result = append(result, link)
	}
	return result, nil
}
