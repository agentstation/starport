package catalog

import (
	"math"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// ModerationValuation projects the complete synchronous text moderation charge.
// A missing declaration or price is unknown. An explicit zero price is free.
func ModerationValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Moderations == nil || billing.Validate() != nil || pricing == nil || pricing.Validate() != nil || pricing.Currency != catalogs.ModelPricingCurrencyUSD || !pricing.IsEffectiveAt(at) || len(pricing.Tiers) != 0 || pricing.Operations == nil || pricing.Operations.Request == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	value := *pricing.Operations.Request
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	return reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: requestBillingUnit, Price: reservation.Price{USD: strconv.FormatFloat(value, 'g', -1, 64), PerUnits: 1}}}}, nil
}
