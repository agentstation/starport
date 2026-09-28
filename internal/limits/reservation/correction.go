package reservation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

const correctionRecordVersion = 2

// Correction binds an authenticated operator decision to one inspected attempt.
// The caller supplies Actor from its authentication boundary, not request JSON.
type Correction struct {
	ID                string   `json:"id"`
	ExpectedBinding   string   `json:"expected_binding"`
	Actor             string   `json:"actor"`
	EvidenceReference string   `json:"evidence_reference"`
	Reason            string   `json:"reason"`
	Evidence          Evidence `json:"evidence"`
}

// CorrectionReceipt preserves the prior record and its replacement evidence.
// Before.CorrectionID links to the previous receipt without an unbounded record.
type CorrectionReceipt struct {
	PublicationDigest string     `json:"publication_digest,omitempty"`
	Version           int        `json:"version"`
	Correction        Correction `json:"correction"`
	Before            Record     `json:"before"`
	RecordedAt        time.Time  `json:"recorded_at"`
}

// CorrectionBinding identifies the complete state that the operator inspected.
func CorrectionBinding(record Record) string {
	encoded, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (c Correction) valid() bool {
	for _, value := range []string{c.ID, c.ExpectedBinding, c.Actor} {
		if !validID(value) {
			return false
		}
	}
	for _, value := range []string{c.EvidenceReference, c.Reason} {
		if strings.TrimSpace(value) == "" || len(value) > 2048 || strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return false
		}
	}
	return c.Actor != "anonymous" && strings.TrimSpace(c.Actor) != "" && c.Evidence.valid()
}

func correctionKey(attemptID, correctionID string) string {
	return storageKey("correction", [2]string{attemptID, correctionID})
}

// InspectCorrection reads one immutable receipt. It never scans the audit history.
func (r *Repository) InspectCorrection(ctx context.Context, attemptID, correctionID string) (*CorrectionReceipt, error) {
	if !validID(attemptID) || !validID(correctionID) {
		return nil, ErrInvalid
	}
	data, err := r.read(ctx, correctionKey(attemptID, correctionID))
	if err != nil {
		return nil, err
	}
	var receipt CorrectionReceipt
	if json.Unmarshal(data, &receipt) != nil || receipt.Version != correctionRecordVersion || !validPublicationDigest(receipt.PublicationDigest) ||
		!receipt.Correction.valid() || receipt.Correction.ID != correctionID ||
		receipt.Before.Attempt.ID != attemptID || receipt.Before.Version != attemptRecordVersion || receipt.Before.AdmittedAt.IsZero() || receipt.Before.CorrectionID == correctionID ||
		(receipt.Before.State != Settled && receipt.Before.State != Uncertain && receipt.Before.State != Dispatched) ||
		validateAttempt(receipt.Before.Attempt) != nil || len(receipt.Before.Bindings) != len(receipt.Before.Attempt.Rules) || validateBindings(&receipt.Before) != nil ||
		receipt.Correction.ExpectedBinding != CorrectionBinding(receipt.Before) ||
		receipt.RecordedAt.IsZero() || receipt.RecordedAt.Before(receipt.Before.AdmittedAt) {
		return nil, ErrUnavailable
	}
	if _, err := receipt.Before.Attempt.evidenceAmount(&receipt.Correction.Evidence); err != nil {
		return nil, ErrUnavailable
	}
	return &receipt, nil
}

// Correct atomically replaces an accepted charge or resolves uncertain capacity.
// It retains the prior evidence, updates every original window, and clears only
// this attempt's dispute. Overflowed aggregates require separate window repair.
func (r *Repository) Correct(ctx context.Context, id string, correction Correction) (*CorrectionReceipt, error) {
	return r.correct(ctx, id, correction, nil, "")
}

func (r *Repository) correct(ctx context.Context, id string, correction Correction, publication []storage.CompareAndSwapMutation, digest string) (*CorrectionReceipt, error) {
	if !validID(id) || !correction.valid() {
		return nil, ErrInvalid
	}
	for range maxConflicts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		prior, err := r.InspectCorrection(ctx, id, correction.ID)
		if err == nil {
			if !sameCorrection(prior.Correction, correction) || prior.PublicationDigest != digest {
				return nil, ErrIdentityConflict
			}
			return prior, nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		if err := r.checkCorrectionPublication(ctx, publication); err != nil {
			// An exact retry can observe publication after the first receipt read.
			receipt, readErr := r.InspectCorrection(ctx, id, correction.ID)
			if readErr == nil {
				if sameCorrection(receipt.Correction, correction) && receipt.PublicationDigest == digest {
					return receipt, nil
				}
				return nil, ErrIdentityConflict
			}
			if !errors.Is(readErr, storage.ErrNotFound) {
				return nil, readErr
			}
			return nil, err
		}
		record, old, err := r.readRecord(ctx, id)
		if err != nil {
			return nil, err
		}
		if correction.ExpectedBinding != CorrectionBinding(*record) {
			// A concurrent exact retry can commit between the audit and attempt reads.
			receipt, err := r.InspectCorrection(ctx, id, correction.ID)
			if err == nil && sameCorrection(receipt.Correction, correction) && receipt.PublicationDigest == digest {
				return receipt, nil
			}
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return nil, err
			}
			return nil, ErrIdentityConflict
		}
		if record.State != Settled && record.State != Uncertain && record.State != Dispatched {
			return nil, ErrTransition
		}
		if err := r.verifyCorrectionHead(ctx, *record); err != nil {
			return nil, err
		}
		amount, err := record.Attempt.evidenceAmount(&correction.Evidence)
		if err != nil {
			return nil, err
		}
		at, err := r.store.AuthorityTime(ctx)
		if err != nil {
			return nil, err
		}
		if at.Before(record.AdmittedAt) || at.IsZero() {
			return nil, ErrUnavailable
		}
		window, err := correctionWindow(*record, at)
		if err != nil {
			return nil, err
		}
		receipt := CorrectionReceipt{PublicationDigest: digest, Version: correctionRecordVersion, Correction: correction, Before: *record, RecordedAt: at}
		mutations, err := r.correctWindows(ctx, record, correction.Evidence, amount)
		if err != nil {
			_, current, readErr := r.readRecord(ctx, id)
			if readErr != nil {
				return nil, readErr
			}
			if !bytes.Equal(old, current) {
				continue
			}
			return nil, err
		}
		applyCorrection(record, correction, amount, at)
		mutation, err := encodeMutation(storageKey("attempt", id), old, record)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, mutation)
		mutation, err = encodeMutation(correctionKey(id, correction.ID), nil, receipt)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, mutation)
		mutations = append(mutations, publication...)
		err = r.store.CompareAndSwapInWindow(ctx, mutations, window)
		if err == nil {
			return &receipt, nil
		}
		if !errors.Is(err, storage.ErrConflict) {
			return nil, err
		}
	}
	return nil, ErrUnavailable
}

func sameCorrection(first, second Correction) bool {
	// Normalize quantity maps through the evidence contract for exact retries.
	if !sameEvidence(&first.Evidence, &second.Evidence) {
		return false
	}
	return first.ID == second.ID && first.ExpectedBinding == second.ExpectedBinding &&
		first.Actor == second.Actor && first.EvidenceReference == second.EvidenceReference && first.Reason == second.Reason
}

func (r *Repository) correctWindows(ctx context.Context, record *Record, evidence Evidence, amount int64) ([]storage.CompareAndSwapMutation, error) {
	mutations := make([]storage.CompareAndSwapMutation, 0, len(record.Bindings)+2)
	for _, binding := range record.Bindings {
		state, previous, err := r.readWindow(ctx, binding.Rule.Meter, binding.Window)
		if err != nil {
			return nil, err
		}
		if state.HistoryID != binding.Rule.HistoryID || state.Overflow {
			return nil, ErrUnavailable
		}
		actual := amount
		if binding.Rule.Meter.Dimension == limits.DimensionTokens {
			actual = evidence.Tokens
		}
		if record.State == Settled {
			prior, err := record.Attempt.evidenceAmount(record.Evidence)
			if err != nil {
				return nil, err
			}
			if binding.Rule.Meter.Dimension == limits.DimensionTokens {
				prior = record.Evidence.Tokens
			}
			if prior > state.Consumed-state.SeedConsumed {
				return nil, ErrUnavailable
			}
			state.Consumed -= prior
		} else {
			if state.Reserved < binding.Amount {
				return nil, ErrUnavailable
			}
			state.Reserved -= binding.Amount
		}
		consume(state, actual)
		if state.Overflow {
			return nil, ErrOverflow
		}
		if record.DisputeID != "" {
			if state.ActiveDisputes == 0 {
				return nil, ErrUnavailable
			}
			state.ActiveDisputes--
			state.ReconciliationRequired = state.ActiveDisputes > 0
		}
		mutation, err := encodeMutation(meterKey(state.Meter, state.Window), previous, state)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, mutation)
	}
	return mutations, nil
}

func applyCorrection(record *Record, correction Correction, amount int64, at time.Time) {
	if record.SettledAt.IsZero() {
		record.SettledAt = at
	}
	record.State, record.Evidence, record.NanoUSD = Settled, &correction.Evidence, record.Attempt.money(amount)
	if correction.Evidence.NoCharge {
		record.NanoUSD = &amount
	}
	record.Pending, record.Unresolved, record.Reason = nil, nil, ""
	if record.DisputeID != "" {
		record.ResolvedDisputeID = record.DisputeID
	}
	record.DisputeID, record.CorrectionID = "", correction.ID
}

func (r *Repository) verifyCorrectionHead(ctx context.Context, record Record) error {
	if record.CorrectionID == "" {
		return nil
	}
	receipt, err := r.InspectCorrection(ctx, record.Attempt.ID, record.CorrectionID)
	if err != nil {
		return errors.Join(ErrUnavailable, err)
	}
	expected := receipt.Before
	amount, err := expected.Attempt.evidenceAmount(&receipt.Correction.Evidence)
	if err != nil {
		return ErrUnavailable
	}
	applyCorrection(&expected, receipt.Correction, amount, receipt.RecordedAt)
	// New provider evidence can add a dispute after this receipt commits.
	expected.DisputeID = record.DisputeID
	if CorrectionBinding(expected) != CorrectionBinding(record) {
		return ErrUnavailable
	}
	return nil
}

// correctionWindow uses whole seconds conservatively at the storage boundary.
// Unresolved capacity has no correction deadline until its first settlement.
func correctionWindow(record Record, at time.Time) (storage.TimeWindow, error) {
	if record.SettledAt.IsZero() {
		return storage.TimeWindow{}, nil
	}
	window := storage.TimeWindow{Start: record.SettledAt.Truncate(time.Second), End: record.SettledAt.Add(limits.CorrectionHorizon).Truncate(time.Second)}
	if at.Before(record.SettledAt) {
		return storage.TimeWindow{}, ErrUnavailable
	}
	if !at.Before(window.End) {
		return storage.TimeWindow{}, limits.ErrCorrectionExpired
	}
	return window, nil
}

// CheckCorrection reads the current correction deadline without granting a write.
// Correct checks the deadline again atomically with the correction publication.
func (r *Repository) CheckCorrection(ctx context.Context, record Record) error {
	at, err := r.store.AuthorityTime(ctx)
	if err != nil {
		return err
	}
	_, err = correctionWindow(record, at)
	return err
}
