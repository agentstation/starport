package router

import (
	"context"
	"math"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

func embeddingQuote(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.EmbeddingsRequest, retained **catalogs.EmbeddingBilling) admission.QuoteFunc {
	return func(needed admission.Requirements) (admission.Quote, error) {
		if snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID || request == nil {
			return admission.Quote{}, admission.ErrBoundUnknown
		}
		offering, err := snapshot.Offering(runtimecatalog.Route{ProviderID: catalogs.ProviderID(route.ProviderID), ProviderModelID: catalogs.ProviderModelID(route.ProviderModelID)})
		if err != nil {
			return admission.Quote{}, admission.ErrBoundUnknown
		}
		bound, err := embeddingTokenBound(offering.Limits, request.Input)
		if err != nil {
			return admission.Quote{}, err
		}
		quote := admission.Quote{Tokens: &bound}
		if needed.Spend {
			valuation, err := runtimecatalog.EmbeddingValuation(offering, time.Now())
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			quote.Valuation = valuation
			quote.Units = reservation.Quantities{"input": bound}
			if *offering.Billing.Embeddings.RequestCharge {
				quote.Units["request"] = 1
			}
			*retained = offering.Billing.Embeddings
		}
		return quote, nil
	}
}

// embeddingTokenBound reserves the provider's full input limit for each item.
// Token IDs do not reduce this bound because adapters can transform their input.
func embeddingTokenBound(limits *catalogs.ModelLimits, input any) (int64, error) {
	perItem, known := limits.Value(catalogs.ModelLimitInputTokens)
	if known != catalogs.ValueKnown || perItem <= 0 {
		perItem, known = limits.Value(catalogs.ModelLimitContextWindow)
	}
	if known != catalogs.ValueKnown || perItem <= 0 {
		return 0, admission.ErrBoundUnknown
	}
	var count int
	switch values := input.(type) {
	case string:
		if values != "" {
			count = 1
		}
	case []string:
		count = len(values)
		for _, value := range values {
			if value == "" {
				return 0, admission.ErrBoundUnknown
			}
		}
	case []int:
		if len(values) > 0 {
			count = 1
		}
		for _, value := range values {
			if value < 0 {
				return 0, admission.ErrBoundUnknown
			}
		}
	case [][]int:
		count = len(values)
		for _, item := range values {
			if len(item) == 0 {
				return 0, admission.ErrBoundUnknown
			}
			for _, value := range item {
				if value < 0 {
					return 0, admission.ErrBoundUnknown
				}
			}
		}
	default:
		return 0, admission.ErrBoundUnknown
	}
	if count <= 0 || perItem > math.MaxInt64/int64(count) {
		return 0, admission.ErrBoundUnknown
	}
	return perItem * int64(count), nil
}

func finishEmbeddingBudget(ctx context.Context, ticket admission.Ticket, response *connectors.EmbeddingsResponse, billing *catalogs.EmbeddingBilling) error {
	if ticket.ID() == "" {
		return nil
	}
	var evidence *reservation.Evidence
	if tokens, known := response.ReportedInputTokens(); known {
		evidence = &reservation.Evidence{ID: ticket.ID() + ":usage", Tokens: tokens}
		if billing != nil {
			evidence.Quantities = reservation.Quantities{"input": tokens}
			if *billing.RequestCharge {
				evidence.Quantities["request"] = 1
			}
		}
	}
	return ticket.Finish(ctx, evidence)
}
