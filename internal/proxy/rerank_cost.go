package proxy

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/usage"
	"math"
	"strconv"
)

const rerankRequestUnit = "request"

// rerankUsageCost keeps missing measurements separate from an explicit zero.
func rerankUsageCost(snapshot *runtimecatalog.RoutableSnapshot, record usage.Record) (*usage.Cost, string) {
	route, ok := snapshot.ResolveRoute(record.ModelUsed)
	if !ok {
		return nil, usage.CostReasonNoRoute
	}
	offering, err := snapshot.Offering(route)
	if err != nil || offering.Pricing == nil || offering.Pricing.Operations == nil {
		return nil, usage.CostReasonNoPricing
	}
	pricing := offering.Pricing
	if offering.Billing != nil && offering.Billing.Rerank != nil && pricing.Operations.RerankBasis != catalogs.ModelRerankBasisToken {
		return nil, usage.CostReasonNoPricing
	}
	var valuation reservation.Valuation
	var units reservation.Quantities
	switch pricing.Operations.RerankBasis {
	case catalogs.ModelRerankBasisToken:
		if record.SearchUnits != 0 {
			return nil, usage.CostReasonRerankUnpriced
		}
		if record.TokensUnknown || record.TokensEstimated {
			return nil, usage.CostReasonNoUsage
		}
		if record.Tokens.Input < 0 || record.Tokens.Total != record.Tokens.Input || record.Tokens.Output != 0 {
			return nil, usage.CostReasonInvalidUsage
		}
		valuation, err = runtimecatalog.RerankValuation(offering, record.Timestamp)
		if err != nil {
			return nil, usage.CostReasonNoPricing
		}
		units = reservation.Quantities{"input": record.Tokens.Input}
		if *offering.Billing.Rerank.RequestCharge {
			units[rerankRequestUnit] = 1
		}
	case catalogs.ModelRerankBasisSearchUnit:
		if !record.SearchUnitsKnown || record.TokensEstimated {
			return nil, usage.CostReasonNoUsage
		}
		if pricing.Validate() != nil || pricing.Currency != catalogs.ModelPricingCurrencyUSD || !pricing.IsEffectiveAt(record.Timestamp) || len(pricing.Tiers) != 0 {
			return nil, usage.CostReasonNoPricing
		}
		price := pricing.Operations.SearchUnit
		if price == nil || math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0 {
			return nil, usage.CostReasonRerankUnpriced
		}
		valuation = reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "search", Price: reservation.Price{USD: strconv.FormatFloat(*price, 'g', -1, 64), PerUnits: 1}}}}
		units = reservation.Quantities{"search": record.SearchUnits}
	default:
		return nil, usage.CostReasonRerankUnpriced
	}
	amount, err := valuation.NanoUSD(units)
	if err != nil {
		return nil, usage.CostReasonInvalidUsage
	}
	return &usage.Cost{NanoUSD: amount, Currency: usageCurrency}, ""
}
