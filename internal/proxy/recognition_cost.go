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
		if offering.Pricing.Operations == nil || offering.Pricing.Operations.PageInput == nil {
			return nil, usage.CostReasonNoPricing
		}
		total = float64(pages) * *offering.Pricing.Operations.PageInput
	case catalogs.RecognitionBillingTokens:
		if measured == nil || measured.Estimated {
			return nil, usage.CostReasonNoUsage
		}
		tokens := usageTokens(*measured)
		if !validRecognitionTokens(tokens) {
			return nil, usage.CostReasonInvalidUsage
		}
		rates := offering.Pricing.Tokens
		var selectedThreshold int64
		for _, tier := range offering.Pricing.Tiers {
			if tier.Type == catalogs.ModelPricingTierTypeContext && tokens.Input > tier.Size && tier.Size > selectedThreshold {
				selectedThreshold = tier.Size
				rates = tier.Tokens
			}
		}
		if rates == nil {
			return nil, usage.CostReasonNoPricing
		}
		if _, known := recognitionTokenRate(rates.Input); !known {
			return nil, usage.CostReasonNoPricing
		}
		if _, known := recognitionTokenRate(rates.Output); !known {
			return nil, usage.CostReasonNoPricing
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
				return nil, usage.CostReasonNoPricing
			}
			total += float64(item.count) * rate
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
