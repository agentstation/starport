package catalog

import (
	"math"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// TextChatValuation projects every declared charge from one selected offering.
// Missing prices, non-USD currencies, and unresolved pricing tiers remain unknown.
// The caller must enforce the text-chat request scope before dispatch.
func TextChatValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.TextChat == nil || billing.Validate() != nil || pricing == nil ||
		pricing.Validate() != nil || pricing.Currency != catalogs.ModelPricingCurrencyUSD ||
		!pricing.IsEffectiveAt(at) || len(pricing.Tiers) != 0 || pricing.Tokens == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	result := reservation.Valuation{Version: reservation.ArithmeticVersion}
	for _, classes := range [][]catalogs.TokenBillingClass{billing.TextChat.Input, billing.TextChat.Output} {
		for _, class := range classes {
			price, err := exactTokenPrice(tokenClassPrice(pricing.Tokens, class))
			if err != nil {
				return reservation.Valuation{}, err
			}
			result.Components = append(result.Components, reservation.Component{Unit: string(class), Price: price})
		}
	}
	if *billing.TextChat.RequestCharge {
		if pricing.Operations == nil || pricing.Operations.Request == nil {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		value := *pricing.Operations.Request
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, reservation.Component{Unit: "request", Price: reservation.Price{USD: strconv.FormatFloat(value, 'g', -1, 64), PerUnits: 1}})
	}
	return result, nil
}

func tokenClassPrice(pricing *catalogs.ModelTokenPricing, class catalogs.TokenBillingClass) *catalogs.ModelTokenCost {
	switch class {
	case catalogs.TokenBillingInput:
		return pricing.Input
	case catalogs.TokenBillingCacheRead:
		return pricing.CacheRead
	case catalogs.TokenBillingCacheWrite:
		return pricing.CacheWrite
	case catalogs.TokenBillingOutput:
		return pricing.Output
	case catalogs.TokenBillingReasoning:
		return pricing.Reasoning
	default:
		return nil
	}
}

func exactTokenPrice(cost *catalogs.ModelTokenCost) (reservation.Price, error) {
	if cost == nil {
		return reservation.Price{}, reservation.ErrValuation
	}
	for _, unit := range []struct {
		kind  catalogs.TokenCostUnit
		count int64
	}{{catalogs.CostUnitPerToken, 1}, {catalogs.CostUnitPerMillion, 1_000_000}} {
		value, presence := cost.Amount(unit.kind)
		if presence != catalogs.ValueKnown {
			continue
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return reservation.Price{}, reservation.ErrValuation
		}
		// Preserve the catalog's decimal representation. Reservation arithmetic
		// multiplies exactly and rounds upward once for the attempt.
		return reservation.Price{USD: strconv.FormatFloat(value, 'g', -1, 64), PerUnits: unit.count}, nil
	}
	return reservation.Price{}, reservation.ErrValuation
}
