package catalog

import (
	"math"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// RecognitionValuation prices the complete synchronous PDF recognition contract.
// Display estimates and unresolved tiers cannot establish a reservation price.
func RecognitionValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	b, p := offering.Billing, offering.Pricing
	if b == nil || b.Recognition == nil || b.Validate() != nil || b.Recognition.RequestCharge == nil || p == nil || p.Validate() != nil || p.Currency != catalogs.ModelPricingCurrencyUSD || !p.IsEffectiveAt(at) || len(p.Tiers) != 0 {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	r := b.Recognition
	if r.Basis == catalogs.RecognitionBillingTokens {
		copied := *b
		copied.TextChat = &catalogs.TextChatBilling{Input: r.Input, Output: r.Output, RequestCharge: r.RequestCharge}
		offering.Billing = &copied
		return TextChatValuation(offering, at)
	}
	if r.Basis != catalogs.RecognitionBillingPages || p.Operations == nil || p.Operations.PageInput == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	result := reservation.Valuation{Version: reservation.ArithmeticVersion}
	for _, unit := range []struct {
		name  string
		value *float64
	}{{"page", p.Operations.PageInput}, {requestBillingUnit, p.Operations.Request}} {
		if unit.name == requestBillingUnit && !*r.RequestCharge {
			continue
		}
		if unit.value == nil || math.IsNaN(*unit.value) || math.IsInf(*unit.value, 0) || *unit.value < 0 {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, reservation.Component{Unit: unit.name, Price: reservation.Price{USD: strconv.FormatFloat(*unit.value, 'g', -1, 64), PerUnits: 1}})
	}
	return result, nil
}
