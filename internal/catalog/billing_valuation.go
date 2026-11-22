package catalog

import (
	"math"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// SpeechCharacterUnit identifies the input count in a character-priced speech valuation.
const SpeechCharacterUnit = "characters"

func validFlatUSDPricing(p *catalogs.ModelPricing, at time.Time) bool {
	return p != nil &&
		p.Validate() == nil &&
		p.Currency == catalogs.ModelPricingCurrencyUSD &&
		p.IsEffectiveAt(at) &&
		len(p.Tiers) == 0
}

func unitPriceComponent(unit string, value *float64) (reservation.Component, bool) {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return reservation.Component{}, false
	}
	return reservation.Component{
		Unit:  unit,
		Price: reservation.Price{USD: strconv.FormatFloat(*value, 'g', -1, 64), PerUnits: 1},
	}, true
}

// inputTokenValuation projects input tokens and an explicitly declared request fee.
// The caller validates the operation's complete billing contract first.
func inputTokenValuation(pricing *catalogs.ModelPricing, requestCharge bool, at time.Time) (reservation.Valuation, error) {
	if !validFlatUSDPricing(pricing, at) || pricing.Tokens == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	price, err := exactTokenPrice(pricing.Tokens.Input)
	if err != nil {
		return reservation.Valuation{}, err
	}
	result := reservation.Valuation{
		Version:    reservation.ArithmeticVersion,
		Components: []reservation.Component{{Unit: string(catalogs.TokenBillingInput), Price: price}},
	}
	if requestCharge {
		if pricing.Operations == nil {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		component, ok := unitPriceComponent(requestBillingUnit, pricing.Operations.Request)
		if !ok {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, component)
	}
	return result, nil
}

// EmbeddingValuation projects the complete synchronous embedding charge.
// The caller must enforce the text or token-ID input scope before dispatch.
func EmbeddingValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Embeddings == nil || billing.Validate() != nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	return inputTokenValuation(pricing, *billing.Embeddings.RequestCharge, at)
}

// ModerationValuation projects the complete synchronous text moderation charge.
// A missing declaration or price is unknown. An explicit zero price is free.
func ModerationValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Moderations == nil || billing.Validate() != nil || !validFlatUSDPricing(pricing, at) || pricing.Operations == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	component, ok := unitPriceComponent(requestBillingUnit, pricing.Operations.Request)
	if !ok {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	return reservation.Valuation{
		Version:    reservation.ArithmeticVersion,
		Components: []reservation.Component{component},
	}, nil
}

// RerankValuation projects every declared charge for synchronous text reranking.
func RerankValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Rerank == nil || billing.Validate() != nil || pricing == nil || pricing.Operations == nil || pricing.Operations.RerankBasis != catalogs.ModelRerankBasisToken {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	return inputTokenValuation(pricing, *billing.Rerank.RequestCharge, at)
}

// SpeechValuation projects all charges declared for character-priced speech.
func SpeechValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Speech == nil || billing.Validate() != nil || !validFlatUSDPricing(pricing, at) || pricing.Operations == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	charComponent, ok := unitPriceComponent(SpeechCharacterUnit, pricing.Operations.CharacterInput)
	if !ok {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	result := reservation.Valuation{
		Version:    reservation.ArithmeticVersion,
		Components: []reservation.Component{charComponent},
	}
	if *billing.Speech.RequestCharge {
		reqComponent, ok := unitPriceComponent(requestBillingUnit, pricing.Operations.Request)
		if !ok {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, reqComponent)
	}
	return result, nil
}

// RecognitionValuation prices the complete synchronous PDF recognition contract.
// Display estimates and unresolved tiers cannot establish a reservation price.
func RecognitionValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	b, p := offering.Billing, offering.Pricing
	if b == nil || b.Recognition == nil || b.Validate() != nil || b.Recognition.RequestCharge == nil || !validFlatUSDPricing(p, at) {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	r := b.Recognition
	if r.Basis == catalogs.RecognitionBillingTokens {
		copied := *b
		copied.TextChat = &catalogs.TextChatBilling{Input: r.Input, Output: r.Output, RequestCharge: r.RequestCharge}
		offering.Billing = &copied
		return TextChatValuation(offering, at)
	}
	if r.Basis != catalogs.RecognitionBillingPages || p.Operations == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	pageComponent, ok := unitPriceComponent("page", p.Operations.PageInput)
	if !ok {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	result := reservation.Valuation{
		Version:    reservation.ArithmeticVersion,
		Components: []reservation.Component{pageComponent},
	}
	if *r.RequestCharge {
		reqComponent, ok := unitPriceComponent(requestBillingUnit, p.Operations.Request)
		if !ok {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, reqComponent)
	}
	return result, nil
}
