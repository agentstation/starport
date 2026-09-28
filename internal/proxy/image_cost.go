package proxy

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/usage"
)

// imageUsageCost prices dedicated image operations using their complete contract.
func imageUsageCost(snapshot *runtimecatalog.RoutableSnapshot, record usage.Record) (*usage.Cost, string) {
	if record.Media == nil || record.Media.GeneratedImages == 0 {
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
	operation := catalogs.ProviderOperationImagesGenerations
	if record.Media.ImagesEdited {
		operation = catalogs.ProviderOperationImagesEdits
	}
	valuation, err := runtimecatalog.ImageValuation(offering, operation, record.Timestamp)
	if err != nil {
		return nil, usage.CostReasonMediaUnpriced
	}
	units, err := runtimecatalog.ImageQuantities(offering.Billing.Images, record.Media.GeneratedImages, record.Media.ImageSize)
	if err != nil {
		return nil, usage.CostReasonInvalidUsage
	}
	amount, err := valuation.NanoUSD(units)
	if err != nil {
		return nil, usage.CostReasonInvalidUsage
	}
	return &usage.Cost{NanoUSD: amount, Currency: usageCurrency}, ""
}
