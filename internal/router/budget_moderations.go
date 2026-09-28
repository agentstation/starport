package router

import (
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

func moderationCharge(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.ModerationRequest) operationCharge[*connectors.ModerationResponse] {
	return operationCharge[*connectors.ModerationResponse]{
		quote: func(needed admission.Requirements) (admission.Quote, error) {
			// A price per request does not establish token consumption.
			if needed.Tokens || snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID || request == nil || len(request.Inputs) == 0 {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			offering, err := snapshot.Offering(runtimecatalog.Route{ProviderID: catalogs.ProviderID(route.ProviderID), ProviderModelID: catalogs.ProviderModelID(route.ProviderModelID)})
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			valuation, err := runtimecatalog.ModerationValuation(offering, time.Now())
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			return admission.Quote{Valuation: valuation, Units: reservation.Quantities{"request": 1}}, nil
		},
		evidence: func(response *connectors.ModerationResponse, err error) *reservation.Evidence {
			if response == nil || err != nil {
				return nil
			}
			return &reservation.Evidence{Quantities: reservation.Quantities{"request": 1}}
		},
	}
}
