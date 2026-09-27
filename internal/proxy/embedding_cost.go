package proxy

import (
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/usage"
)

// embeddingUsageCost uses the same declared rates as embedding admission.
// Optional reporting cannot replace the durable reservation ledger.
func embeddingUsageCost(snapshot *runtimecatalog.RoutableSnapshot, record usage.Record) (*usage.Cost, string) {
	if record.TokensUnknown || record.TokensEstimated {
		return nil, usage.CostReasonNoUsage
	}
	tokens := record.Tokens
	if tokens.Input < 0 || tokens.Input != tokens.Total || tokens.Output != 0 {
		return nil, usage.CostReasonInvalidUsage
	}
	route, ok := snapshot.ResolveRoute(record.ModelUsed)
	if !ok {
		return nil, usage.CostReasonNoRoute
	}
	offering, err := snapshot.Offering(route)
	if err != nil {
		return nil, usage.CostReasonNoPricing
	}
	valuation, err := runtimecatalog.EmbeddingValuation(offering, record.Timestamp)
	if err != nil {
		return nil, usage.CostReasonNoPricing
	}
	quantities := reservation.Quantities{"input": tokens.Input}
	if *offering.Billing.Embeddings.RequestCharge {
		quantities["request"] = 1
	}
	total, err := valuation.NanoUSD(quantities)
	if err != nil {
		return nil, usage.CostReasonInvalidUsage
	}
	return &usage.Cost{NanoUSD: total, Currency: usageCurrency}, ""
}
