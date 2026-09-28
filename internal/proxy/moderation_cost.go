package proxy

import (
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/usage"
)

const moderationRequestUnit = "request"

// moderationUsageCost reports a completed request at its declared catalog price.
// Token use remains unknown when the provider does not measure it.
func moderationUsageCost(snapshot *runtimecatalog.RoutableSnapshot, record usage.Record) (*usage.Cost, string) {
	if record.Status != usage.StatusOK {
		return nil, usage.CostReasonNoUsage
	}
	route, ok := snapshot.ResolveRoute(record.ModelUsed)
	if !ok {
		return nil, usage.CostReasonNoRoute
	}
	offering, err := snapshot.Offering(route)
	if err != nil {
		return nil, usage.CostReasonNoPricing
	}
	valuation, err := runtimecatalog.ModerationValuation(offering, record.Timestamp)
	if err != nil {
		return nil, usage.CostReasonNoPricing
	}
	amount, err := valuation.NanoUSD(reservation.Quantities{moderationRequestUnit: 1})
	if err != nil {
		return nil, usage.CostReasonInvalidUsage
	}
	return &usage.Cost{NanoUSD: amount, Currency: usageCurrency}, ""
}
