package catalog

import (
	"math"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// EmbeddingValuation projects the complete synchronous embedding charge.
// The caller must enforce the text or token-ID input scope before dispatch.
func EmbeddingValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Embeddings == nil || billing.Validate() != nil || pricing == nil || pricing.Validate() != nil || pricing.Currency != catalogs.ModelPricingCurrencyUSD || !pricing.IsEffectiveAt(at) || len(pricing.Tiers) != 0 || pricing.Tokens == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	price, err := exactTokenPrice(pricing.Tokens.Input)
	if err != nil {
		return reservation.Valuation{}, err
	}
	result := reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: string(catalogs.TokenBillingInput), Price: price}}}
	if *billing.Embeddings.RequestCharge {
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
