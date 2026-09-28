package router

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
	"time"
)

func rerankCharge(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.RerankRequest) operationCharge[*connectors.RerankResponse] {
	var billing *catalogs.RerankBilling
	return operationCharge[*connectors.RerankResponse]{
		quote: func(needed admission.Requirements) (admission.Quote, error) {
			if snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			offering, err := snapshot.Offering(runtimecatalog.Route{ProviderID: catalogs.ProviderID(route.ProviderID), ProviderModelID: catalogs.ProviderModelID(route.ProviderModelID)})
			if err != nil || offering.Billing == nil || offering.Billing.Rerank == nil || offering.Billing.Validate() != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			bound, err := rerankTokenBound(offering.Limits, request)
			if err != nil {
				return admission.Quote{}, err
			}
			quote := admission.Quote{Tokens: &bound}
			if needed.Spend {
				quote.Valuation, err = runtimecatalog.RerankValuation(offering, time.Now())
				if err != nil {
					return admission.Quote{}, admission.ErrBoundUnknown
				}
				billing = offering.Billing.Rerank
				quote.Units = reservation.Quantities{string(catalogs.TokenBillingInput): bound}
				if *billing.RequestCharge {
					quote.Units["request"] = 1
				}
			}
			return quote, nil
		},
		evidence: func(response *connectors.RerankResponse, _ error) *reservation.Evidence {
			if response == nil || !response.TokensKnown || response.Usage == nil {
				return nil
			}
			u := response.Usage
			if u.InputTokens < 0 || u.TotalTokens != u.InputTokens || u.OutputTokens != 0 {
				return nil
			}
			result := &reservation.Evidence{Tokens: int64(u.TotalTokens)}
			if billing != nil {
				result.Quantities = reservation.Quantities{string(catalogs.TokenBillingInput): int64(u.InputTokens)}
				if *billing.RequestCharge {
					result.Quantities["request"] = 1
				}
			}
			return result
		},
	}
}

// rerankTokenBound includes the repeated query and every submitted document.
// TopN changes the returned ranking size, not the amount of paid input.
func rerankTokenBound(limits *catalogs.ModelLimits, request *connectors.RerankRequest) (int64, error) {
	if request == nil || request.Query == "" || len(request.Documents) == 0 || request.MaxTokensPerDocument != nil {
		return 0, admission.ErrBoundUnknown
	}
	count := int64(len(request.Documents))
	maxDocuments, known := limits.Value(catalogs.ModelLimitMaxDocuments)
	if known != catalogs.ValueKnown || maxDocuments <= 0 || count > maxDocuments {
		return 0, admission.ErrBoundUnknown
	}
	pair, pairKnown := limits.Value(catalogs.ModelLimitContextWindow)
	total, totalKnown := limits.Value(catalogs.ModelLimitInputTokens)
	if pairKnown != catalogs.ValueKnown || totalKnown != catalogs.ValueKnown || pair <= 0 || total <= 0 {
		return 0, admission.ErrBoundUnknown
	}
	for _, doc := range request.Documents {
		if doc == "" {
			return 0, admission.ErrBoundUnknown
		}
	}
	// Saturate at the request limit before multiplication can overflow.
	if count > total/pair {
		return total, nil
	}
	return count * pair, nil
}
