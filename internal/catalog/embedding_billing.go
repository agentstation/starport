package catalog

import (
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// EmbeddingValuation projects the complete synchronous embedding charge.
// The caller must enforce the text or token-ID input scope before dispatch.
func EmbeddingValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Embeddings == nil || billing.Validate() != nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	return inputTokenValuation(pricing, *billing.Embeddings.RequestCharge, at)
}
