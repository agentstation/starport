package reservation

import "github.com/agentstation/starport/internal/limits"

// amount returns an internal zero placeholder for token-only arithmetic. The
// stored monetary value remains null. Public valuations never treat unknown as free.
func (a Attempt) amount(quantities Quantities) (int64, error) {
	if !a.TokenOnly {
		return a.Valuation.NanoUSD(quantities)
	}
	if a.Valuation.Version != "" || len(a.Valuation.Components) != 0 || len(quantities) != 0 || len(a.Bound) != 0 {
		return 0, ErrValuation
	}
	for _, rule := range a.Rules {
		if rule.Meter.Dimension != limits.DimensionTokens {
			return 0, ErrValuation
		}
	}
	return 0, nil
}

func (a Attempt) money(amount int64) *int64 {
	if a.TokenOnly {
		return nil
	}
	return new(amount)
}

func (r *Record) moneyMatches(amount int64) bool {
	if r.Attempt.TokenOnly {
		return r.NanoUSD == nil
	}
	return r.NanoUSD != nil && *r.NanoUSD == amount
}
