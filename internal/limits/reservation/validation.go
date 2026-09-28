package reservation

import (
	"errors"
	"github.com/agentstation/starport/internal/limits"
)

func validateBindings(record *Record) error {
	amount, err := record.Attempt.amount(record.Attempt.Bound)
	if err != nil {
		return ErrUnavailable
	}
	for i, binding := range record.Bindings {
		rule := record.Attempt.Rules[i]
		expected := amount
		if rule.Meter.Dimension == limits.DimensionTokens {
			expected = record.Attempt.TokenBound
		}
		if binding.Rule != rule || binding.Window != windowFor(rule.Meter.Interval, record.AdmittedAt) || binding.Amount != expected {
			return ErrUnavailable
		}
	}
	return validateSettlement(record, amount)
}

func validateSettlement(record *Record, amount int64) error {
	if (record.State == Settled) != !record.SettledAt.IsZero() || (!record.SettledAt.IsZero() && record.SettledAt.Before(record.AdmittedAt)) {
		return ErrUnavailable
	}
	if record.CorrectionID != "" && (!validID(record.CorrectionID) || record.State != Settled) {
		return ErrUnavailable
	}
	if record.ResolvedDisputeID != "" && (!validID(record.ResolvedDisputeID) || record.CorrectionID == "" || record.ResolvedDisputeID == record.DisputeID) {
		return ErrUnavailable
	}
	if record.DisputeID != "" && (!validID(record.DisputeID) || record.State == Reserved || record.State == Canceled) {
		return ErrUnavailable
	}
	return validateSettlementEvidence(record, amount)
}

func validateSettlementEvidence(record *Record, amount int64) error {
	if record.Pending != nil {
		if record.State != Uncertain || record.Unresolved != nil || !record.Pending.valid() {
			return ErrUnavailable
		}
		_, err := record.Attempt.evidenceAmount(record.Pending)
		if err != nil && !errors.Is(err, ErrOverflow) {
			return ErrUnavailable
		}
	}
	if record.Unresolved != nil {
		if record.State != Uncertain || !record.Unresolved.valid() {
			return ErrUnavailable
		}
		_, err := record.Attempt.evidenceAmount(record.Unresolved)
		if !errors.Is(err, ErrOverflow) {
			return ErrUnavailable
		}
	}
	switch record.State {
	case Reserved, Dispatched, Uncertain:
		if record.Evidence != nil || !record.moneyMatches(amount) {
			return ErrUnavailable
		}
	case Canceled:
		if record.Evidence != nil || !record.moneyMatches(0) {
			return ErrUnavailable
		}
	case Settled:
		if record.Evidence == nil || !record.Evidence.valid() {
			return ErrUnavailable
		}
		actual, err := record.Attempt.evidenceAmount(record.Evidence)
		if err != nil || !record.moneyMatches(actual) {
			return ErrUnavailable
		}
	default:
		return ErrUnavailable
	}
	return nil
}
