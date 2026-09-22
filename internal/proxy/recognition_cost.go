package proxy

import (
	"math"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/usage"
)

// recognitionCost settles measured units against the retained offering.
// It never uses a page estimate as a charge or mixes rates from separate tiers.
func recognitionCost(offering catalogs.ProviderOffering, pages int, measured *inference.Usage, at time.Time) (*usage.Cost, string) {
	if offering.Billing == nil || offering.Billing.Recognition == nil || offering.Billing.Validate() != nil || offering.Pricing == nil || offering.Pricing.Validate() != nil || !offering.Pricing.IsEffectiveAt(at) {
		return nil, usage.CostReasonNoPricing
	}
	if offering.Pricing.Currency != catalogs.ModelPricingCurrencyUSD {
		return nil, usage.CostReasonNoPricing
	}
	var total float64
	switch offering.Billing.Recognition.Basis {
	case catalogs.RecognitionBillingPages:
		if pages <= 0 {
			return nil, usage.CostReasonNoUsage
		}
		input := int64(0)
		if len(offering.Pricing.Tiers) > 0 {
			if measured == nil || measured.Estimated {
				return nil, usage.CostReasonNoUsage
			}
			tokens := usageTokens(*measured)
			if !validRecognitionTokens(tokens) {
				return nil, usage.CostReasonInvalidUsage
			}
			input = tokens.Input
		}
		_, operations := recognitionContextRates(offering.Pricing, input)
		if operations == nil || operations.PageInput == nil {
			return nil, usage.CostReasonNoPricing
		}
		total = float64(pages) * *operations.PageInput
		if operations.Request != nil {
			total += *operations.Request
		}
	case catalogs.RecognitionBillingTokens:
		var reason string
		total, reason = recognitionTokenCost(offering.Pricing, measured)
		if reason != "" {
			return nil, reason
		}
	default:
		return nil, usage.CostReasonNoPricing
	}
	nano := math.Round(total * 1e9)
	if math.IsNaN(nano) || math.IsInf(nano, 0) || nano < 0 || nano >= float64(math.MaxInt64) {
		return nil, usage.CostReasonNoPricing
	}
	return &usage.Cost{NanoUSD: int64(nano), Currency: usageCurrency}, ""
}

func recognitionTokenRate(rate *catalogs.ModelTokenCost) (float64, bool) {
	if rate == nil {
		return 0, false
	}
	if value, state := rate.Amount(catalogs.CostUnitPerToken); state == catalogs.ValueKnown {
		return value, true
	}
	if value, state := rate.Amount(catalogs.CostUnitPerMillion); state == catalogs.ValueKnown {
		return value / 1_000_000, true
	}
	return 0, false
}

func validRecognitionTokens(tokens usage.Tokens) bool {
	for _, count := range []int64{tokens.Input, tokens.Output, tokens.Total, tokens.Reasoning, tokens.CacheRead, tokens.CacheWrite, tokens.AudioInput, tokens.AudioOutput} {
		if count < 0 {
			return false
		}
	}
	if tokens.Input > math.MaxInt64-tokens.Output || tokens.Total != tokens.Input+tokens.Output {
		return false
	}
	// Subtract bounded shares to avoid overflow while checking their sum.
	remaining := tokens.Input
	for _, share := range []int64{tokens.CacheRead, tokens.CacheWrite, tokens.AudioInput} {
		if share > remaining {
			return false
		}
		remaining -= share
	}
	return tokens.AudioOutput <= tokens.Output && tokens.Reasoning <= tokens.Output-tokens.AudioOutput
}

// recognitionTokenCost prices measured token dimensions within one context tier.
func recognitionTokenCost(pricing *catalogs.ModelPricing, measured *inference.Usage) (float64, string) {
	var total float64
	if measured == nil || measured.Estimated {
		return 0, usage.CostReasonNoUsage
	}
	tokens := usageTokens(*measured)
	if !validRecognitionTokens(tokens) {
		return 0, usage.CostReasonInvalidUsage
	}
	rates, operations := recognitionContextRates(pricing, tokens.Input)
	if rates == nil {
		return 0, usage.CostReasonNoPricing
	}
	if _, known := recognitionTokenRate(rates.Input); !known {
		return 0, usage.CostReasonNoPricing
	}
	if _, known := recognitionTokenRate(rates.Output); !known {
		return 0, usage.CostReasonNoPricing
	}
	reasoning := int64(0)
	if rates.Reasoning != nil {
		reasoning = tokens.Reasoning
	}
	for _, item := range []struct {
		count int64
		rate  *catalogs.ModelTokenCost
	}{
		{tokens.Input - tokens.CacheRead - tokens.CacheWrite - tokens.AudioInput, rates.Input},
		{tokens.Output - tokens.AudioOutput - reasoning, rates.Output},
		{reasoning, rates.Reasoning},
		{tokens.CacheRead, rates.CacheRead}, {tokens.CacheWrite, rates.CacheWrite},
		{tokens.AudioInput, rates.AudioInput}, {tokens.AudioOutput, rates.AudioOutput},
	} {
		if item.count == 0 {
			continue
		}
		rate, known := recognitionTokenRate(item.rate)
		if !known {
			return 0, usage.CostReasonNoPricing
		}
		total += float64(item.count) * rate
	}
	if operations != nil && operations.Request != nil {
		total += *operations.Request
	}
	return total, ""
}

// recognitionContextRates selects one complete tier for the measured input size.
func recognitionContextRates(pricing *catalogs.ModelPricing, input int64) (*catalogs.ModelTokenPricing, *catalogs.ModelOperationPricing) {
	tokens, operations := pricing.Tokens, pricing.Operations
	var threshold int64
	for _, tier := range pricing.Tiers {
		if tier.Type == catalogs.ModelPricingTierTypeContext && input > tier.Size && tier.Size > threshold {
			threshold = tier.Size
			tokens, operations = tier.Tokens, tier.Operations
		}
	}
	return tokens, operations
}
