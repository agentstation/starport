package jobs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"

	"github.com/agentstation/starport/internal/storage"
)

const correctionSchemaVersion = 1
const maxCorrectionRecordBytes = 64 << 10
const correctionPrefix = "jobcorrection:v1:account:"

type correctionRecord struct {
	Version int              `json:"version"`
	Intent  CorrectionIntent `json:"intent"`
}

type correctionEvent struct {
	Version int    `json:"version"`
	Account string `json:"account"`
	JobID   string `json:"job_id"`
	ID      string `json:"id"`
	Value   string `json:"value"`
}

func correctionKey(account, job, kind, id string) string {
	encode := base64.RawURLEncoding.EncodeToString
	return correctionPrefix + encode([]byte(account)) + ":job:" + encode([]byte(job)) + ":" + kind + ":" + encode([]byte(id))
}

func encodeCorrection(intent CorrectionIntent) ([]byte, error) {
	data, err := json.Marshal(correctionRecord{Version: correctionSchemaVersion, Intent: intent}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCorrectionRecordBytes {
		return nil, ErrReconciliationInvalid
	}
	return data, nil
}

func decodeCorrection(data []byte, job Job, id string) (CorrectionIntent, error) {
	var record correctionRecord
	if len(data) == 0 || len(data) > maxCorrectionRecordBytes || json.Unmarshal(data, &record) != nil || record.Version != correctionSchemaVersion || record.Intent.Decision.DecisionID != id || !record.Intent.validFor(job) {
		return CorrectionIntent{}, ErrCorruptRecord
	}
	return record.Intent, nil
}

func eventMutation(job Job, kind, id, value string) (storage.CompareAndSwapMutation, error) {
	data, err := json.Marshal(correctionEvent{Version: correctionSchemaVersion, Account: job.Account, JobID: job.ID, ID: id, Value: value}, json.Deterministic(true))
	return storage.CompareAndSwapMutation{Key: correctionKey(job.Account, job.ID, kind, id), NewValue: data}, err
}

func decodeCorrectionEvent(data []byte, job Job, id string) (string, error) {
	var record correctionEvent
	if len(data) == 0 || len(data) > maxCorrectionRecordBytes || json.Unmarshal(data, &record) != nil || record.Version != correctionSchemaVersion || record.Account != job.Account || record.JobID != job.ID || record.ID != id || !reconciliationText(record.Value, 128) {
		return "", ErrCorruptRecord
	}
	return record.Value, nil
}

func (r *repository) correctionIntent(ctx context.Context, job Job, id string) (CorrectionIntent, error) {
	data, err := r.store.Get(ctx, correctionKey(job.Account, job.ID, "intent", id))
	if errors.Is(err, storage.ErrNotFound) {
		return CorrectionIntent{}, ErrCorrectionNotFound
	}
	if err != nil {
		return CorrectionIntent{}, err
	}
	return decodeCorrection(data, job, id)
}

// verifyCorrectionHeads refuses a missing audit before a state change.
func (r *repository) verifyCorrectionHeads(ctx context.Context, job Job) error {
	for _, head := range []*CorrectionIntent{job.correctionHead, job.correctionApplied} {
		if head == nil {
			continue
		}
		intent, err := r.correctionIntent(ctx, job, head.Decision.DecisionID)
		if err != nil {
			return errors.Join(ErrCorruptRecord, err)
		}
		if !sameCorrectionIntent(intent, *head) {
			return ErrCorruptRecord
		}
	}
	if job.correctionApplied != nil {
		data, err := r.store.Get(ctx, correctionKey(job.Account, job.ID, "applied", correctionID(job.correctionApplied)))
		if err != nil {
			return errors.Join(ErrCorruptRecord, err)
		}
		expected, err := encodeCorrection(*job.correctionApplied)
		if err != nil || !bytes.Equal(data, expected) {
			return ErrCorruptRecord
		}
	}
	if job.correctionReported != "" {
		data, err := r.store.Get(ctx, correctionKey(job.Account, job.ID, "reported", job.correctionReported))
		if err != nil {
			return errors.Join(ErrCorruptRecord, err)
		}
		status, err := decodeCorrectionEvent(data, job, job.correctionReported)
		if err != nil || (status != correctionReportDelivered && status != correctionReportExpired && status != correctionReportDisabled) {
			return ErrCorruptRecord
		}
	}
	return nil
}

// CreateCorrection stores immutable intent and its history link with the job head.
func (r *repository) CreateCorrection(ctx context.Context, expected Job, intent CorrectionIntent) (Job, error) {
	if (expected.correctionHead != nil && intent.Decision.DecidedAt.Before(expected.correctionHead.Decision.DecidedAt)) || !intent.validFor(expected) || intent.PreviousID != correctionID(expected.correctionHead) || intent.PreviousAppliedID != correctionID(expected.correctionApplied) || intent.EvidenceBinding != expected.correctionEvidenceBinding() || intent.Decision.Binding != expected.CorrectionBinding(intent.BudgetBinding) {
		return expected, ErrReconciliationInvalid
	}
	if err := r.verifyCorrectionHeads(ctx, expected); err != nil {
		return expected, err
	}
	next := expected
	next.correctionHead = copyCorrection(&intent)
	mutation, err := r.replacement(ctx, expected, next)
	if err != nil {
		return expected, err
	}
	data, err := encodeCorrection(intent)
	if err != nil {
		return expected, err
	}
	link, err := eventMutation(expected, "history-next", intent.PreviousID, intent.Decision.DecisionID)
	if err != nil {
		return expected, err
	}
	changes := []storage.CompareAndSwapMutation{mutation, {Key: correctionKey(intent.Account, intent.JobID, "intent", intent.Decision.DecisionID), NewValue: data}, link}
	if err := r.store.CompareAndSwapBatch(ctx, changes); err != nil {
		return expected, err
	}
	return next, nil
}

// InspectCorrection reads one immutable decision and its outcome markers.
func (r *repository) InspectCorrection(ctx context.Context, account, jobID, id string) (CorrectionAudit, error) {
	if !reconciliationText(id, 128) {
		return CorrectionAudit{}, ErrReconciliationInvalid
	}
	job, err := r.Get(ctx, account, jobID)
	if err != nil {
		return CorrectionAudit{}, err
	}
	intent, err := r.correctionIntent(ctx, job, id)
	if err != nil {
		return CorrectionAudit{}, err
	}
	result := CorrectionAudit{Intent: intent}
	kinds := []string{"applied", "reported", "history-next"}
	keys := make([]string, len(kinds))
	for i, kind := range kinds {
		keys[i] = correctionKey(account, jobID, kind, id)
	}
	values, err := r.store.BatchGet(ctx, keys)
	if err != nil {
		return result, err
	}
	if data, exists := values[keys[0]]; exists {
		expected, err := encodeCorrection(intent)
		if err != nil || !bytes.Equal(data, expected) {
			return result, ErrCorruptRecord
		}
		result.Applied = true
	}
	if data, exists := values[keys[1]]; exists {
		status, err := decodeCorrectionEvent(data, job, id)
		if err != nil || !result.Applied || (status != correctionReportDelivered && status != correctionReportExpired && status != correctionReportDisabled) {
			return result, ErrCorruptRecord
		}
		result.ReportStatus = status
	}
	if data, exists := values[keys[2]]; exists && !result.Applied {
		nextID, err := decodeCorrectionEvent(data, job, id)
		if err != nil {
			return result, err
		}
		successor, err := r.correctionIntent(ctx, job, nextID)
		if err != nil || successor.PreviousID != id {
			return result, ErrCorruptRecord
		}
		result.SupersededBy = nextID
	}
	return result, nil
}

// ApplyCorrection publishes effective state and a forward report link atomically.
func (r *repository) ApplyCorrection(ctx context.Context, expected Job, commit CorrectionCommit) (Job, error) {
	intent := expected.correctionHead
	if intent == nil || correctionID(intent) == correctionID(expected.correctionApplied) {
		return expected, ErrReconciliationConflict
	}
	if intent.EvidenceBinding != expected.correctionEvidenceBinding() {
		return expected, ErrReconciliationConflict
	}
	if expected.ReservationID != "" && commit == nil {
		return expected, ErrSettlementPending
	}
	if err := r.verifyCorrectionHeads(ctx, expected); err != nil {
		return expected, err
	}
	next := expected
	next.correctionApplied = copyCorrection(intent)
	mutation, err := r.replacement(ctx, expected, next)
	if err != nil {
		return expected, err
	}
	data, err := encodeCorrection(*intent)
	if err != nil {
		return expected, err
	}
	link, err := eventMutation(expected, "applied-next", intent.PreviousAppliedID, intent.Decision.DecisionID)
	if err != nil {
		return expected, err
	}
	changes := []storage.CompareAndSwapMutation{mutation, {Key: correctionKey(intent.Account, intent.JobID, "applied", intent.Decision.DecisionID), NewValue: data}, link}
	if commit != nil {
		err = commit(ctx, *copyCorrection(intent), changes)
	} else {
		err = r.store.CompareAndSwapBatch(ctx, changes)
	}
	if err != nil {
		return expected, err
	}
	return next, nil
}

// NextCorrectionReport returns the next applied decision after the durable cursor.
// A forward link keeps recovery bounded even when optional reports remain unavailable.
func (r *repository) NextCorrectionReport(ctx context.Context, job Job) (*CorrectionIntent, error) {
	if err := r.verifyCorrectionHeads(ctx, job); err != nil {
		return nil, err
	}
	if job.correctionApplied == nil || job.correctionReported == correctionID(job.correctionApplied) {
		return nil, nil
	}
	data, err := r.store.Get(ctx, correctionKey(job.Account, job.ID, "applied-next", job.correctionReported))
	if err != nil {
		return nil, errors.Join(ErrCorruptRecord, err)
	}
	id, err := decodeCorrectionEvent(data, job, job.correctionReported)
	if err != nil {
		return nil, err
	}
	intent, err := r.correctionIntent(ctx, job, id)
	if err != nil {
		return nil, err
	}
	if intent.PreviousAppliedID != job.correctionReported {
		return nil, ErrCorruptRecord
	}
	applied, err := r.store.Get(ctx, correctionKey(job.Account, job.ID, "applied", id))
	if err != nil {
		return nil, errors.Join(ErrCorruptRecord, err)
	}
	encoded, err := encodeCorrection(intent)
	if err != nil || !bytes.Equal(applied, encoded) {
		return nil, ErrCorruptRecord
	}
	return copyCorrection(&intent), nil
}

// MarkCorrectionReported retains one report result without changing required billing.
func (r *repository) MarkCorrectionReported(ctx context.Context, expected Job, intent CorrectionIntent, status string) (Job, error) {
	if status != correctionReportDelivered && status != correctionReportExpired && status != correctionReportDisabled {
		return expected, ErrReconciliationInvalid
	}
	pending, err := r.NextCorrectionReport(ctx, expected)
	if err != nil {
		return expected, err
	}
	if pending == nil || !sameCorrectionIntent(*pending, intent) {
		return expected, ErrReconciliationConflict
	}
	next := expected
	next.correctionReported = intent.Decision.DecisionID
	mutation, err := r.replacement(ctx, expected, next)
	if err != nil {
		return expected, err
	}
	receipt, err := eventMutation(expected, "reported", intent.Decision.DecisionID, status)
	if err != nil {
		return expected, err
	}
	if err := r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{mutation, receipt}); err != nil {
		return expected, err
	}
	return next, nil
}
