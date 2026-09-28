package proxy

import (
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/usage"
)

// speechUsageCost uses the declared input character unit independently of tokens.
func speechUsageCost(snapshot *runtimecatalog.RoutableSnapshot, record usage.Record) (*usage.Cost, string) {
	if !record.InputCharactersKnown {
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
	valuation, err := runtimecatalog.SpeechValuation(offering, record.Timestamp)
	if err != nil {
		return nil, usage.CostReasonNoPricing
	}
	units := reservation.Quantities{runtimecatalog.SpeechCharacterUnit: record.InputCharacters}
	if *offering.Billing.Speech.RequestCharge {
		units["request"] = 1
	}
	amount, err := valuation.NanoUSD(units)
	if err != nil {
		return nil, usage.CostReasonInvalidUsage
	}
	return &usage.Cost{NanoUSD: amount, Currency: usageCurrency}, ""
}
