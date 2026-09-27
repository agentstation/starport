package reservation

import (
	"errors"
	"github.com/agentstation/starport/internal/limits"
)

func validateBindings(record *Record) error {
	amount, err := record.Attempt.Valuation.NanoUSD(record.Attempt.Bound)
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
	if record.Unresolved != nil {
		if record.State != Uncertain || !validID(record.Unresolved.ID) || record.Unresolved.Tokens < 0 {
			return ErrUnavailable
		}
		_, err := record.Attempt.Valuation.NanoUSD(record.Unresolved.Quantities)
		if !errors.Is(err, ErrOverflow) {
			return ErrUnavailable
		}
	}
	switch record.State {
	case Reserved, Dispatched, Uncertain:
		if record.Evidence != nil || record.NanoUSD != amount {
			return ErrUnavailable
		}
	case Canceled:
		if record.Evidence != nil || record.NanoUSD != 0 {
			return ErrUnavailable
		}
	case Settled:
		if record.Evidence == nil || !validID(record.Evidence.ID) || record.Evidence.Tokens < 0 {
			return ErrUnavailable
		}
		actual, err := record.Attempt.Valuation.NanoUSD(record.Evidence.Quantities)
		if err != nil || actual != record.NanoUSD {
			return ErrUnavailable
		}
	default:
		return ErrUnavailable
	}
	return nil
}
