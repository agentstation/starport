package catalog

import (
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// RecognitionOfferingFor returns the exact recognition offering used by a route.
// Its billing units and rates come from the request's retained catalog generation.
func (s *RoutableSnapshot) RecognitionOfferingFor(modelID string) (catalogs.ProviderOffering, bool) {
	route, found := s.ResolveOperation(modelID, catalogs.ProviderOperationDocumentsRecognition)
	if !found {
		return catalogs.ProviderOffering{}, false
	}
	offering, err := s.Offering(route)
	return offering, err == nil
}

func billableRecognition(offering catalogs.ProviderOffering) error {
	unpriced := fmt.Errorf("%w: %s/%s", ErrRecognitionUnpriced, offering.ProviderID, offering.ProviderModelID)
	if offering.Billing == nil || offering.Billing.Recognition == nil || offering.Pricing == nil {
		return unpriced
	}
	if offering.Billing.Validate() != nil || offering.Pricing.Validate() != nil {
		return unpriced
	}
	// Starport settles USD. Another currency requires an explicit conversion policy.
	if offering.Pricing.Currency != catalogs.ModelPricingCurrencyUSD {
		return unpriced
	}
	switch offering.Billing.Recognition.Basis {
	case catalogs.RecognitionBillingPages:
		if offering.Pricing.Operations == nil || offering.Pricing.Operations.PageInput == nil {
			return unpriced
		}
	case catalogs.RecognitionBillingTokens:
		rates := offering.Pricing.Tokens
		if rates == nil || !knownTokenRate(rates.Input) || !knownTokenRate(rates.Output) {
			return unpriced
		}
	default:
		return unpriced
	}
	return nil
}

func knownTokenRate(rate *catalogs.ModelTokenCost) bool {
	if rate == nil {
		return false
	}
	_, presence := rate.Amount(catalogs.CostUnitPerToken)
	if presence == catalogs.ValueKnown {
		return true
	}
	_, presence = rate.Amount(catalogs.CostUnitPerMillion)
	return presence == catalogs.ValueKnown
}
