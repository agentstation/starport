package catalog

import (
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// RerankValuation projects every declared charge for synchronous text reranking.
func RerankValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Rerank == nil || billing.Validate() != nil || pricing == nil || pricing.Operations == nil || pricing.Operations.RerankBasis != catalogs.ModelRerankBasisToken {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	return inputTokenValuation(pricing, *billing.Rerank.RequestCharge, at)
}
