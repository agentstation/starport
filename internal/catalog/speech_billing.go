package catalog

import (
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// SpeechCharacterUnit identifies the input count in a character-priced speech valuation.
const SpeechCharacterUnit = "characters"

// SpeechValuation projects all charges declared for character-priced speech.
func SpeechValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Speech == nil || billing.Validate() != nil || pricing == nil || pricing.Validate() != nil || pricing.Currency != catalogs.ModelPricingCurrencyUSD || !pricing.IsEffectiveAt(at) || len(pricing.Tiers) != 0 || pricing.Operations == nil || pricing.Operations.CharacterInput == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	result := reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: SpeechCharacterUnit, Price: reservation.Price{USD: strconv.FormatFloat(*pricing.Operations.CharacterInput, 'g', -1, 64), PerUnits: 1}}}}
	if *billing.Speech.RequestCharge {
		if pricing.Operations.Request == nil {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, reservation.Component{Unit: requestBillingUnit, Price: reservation.Price{USD: strconv.FormatFloat(*pricing.Operations.Request, 'g', -1, 64), PerUnits: 1}})
	}
	return result, nil
}
