package catalog

import (
	"math"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// inputTokenValuation projects input tokens and an explicitly declared request fee.
// The caller validates the operation's complete billing contract first.
func inputTokenValuation(pricing *catalogs.ModelPricing, requestCharge bool, at time.Time) (reservation.Valuation, error) {
	if pricing == nil || pricing.Validate() != nil || pricing.Currency != catalogs.ModelPricingCurrencyUSD || !pricing.IsEffectiveAt(at) || len(pricing.Tiers) != 0 || pricing.Tokens == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	price, err := exactTokenPrice(pricing.Tokens.Input)
	if err != nil {
		return reservation.Valuation{}, err
	}
	result := reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: string(catalogs.TokenBillingInput), Price: price}}}
	if requestCharge {
		if pricing.Operations == nil || pricing.Operations.Request == nil {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		value := *pricing.Operations.Request
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, reservation.Component{Unit: requestBillingUnit, Price: reservation.Price{USD: strconv.FormatFloat(value, 'g', -1, 64), PerUnits: 1}})
	}
	return result, nil
}
