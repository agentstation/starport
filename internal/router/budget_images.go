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

func imageCharge(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.ImagesRequest) operationCharge[*connectors.ImagesResponse] {
	var billing *catalogs.ImageBilling
	return operationCharge[*connectors.ImagesResponse]{
		quote: func(needed admission.Requirements) (admission.Quote, error) {
			if needed.Tokens || snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID || request == nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			offering, err := snapshot.Offering(runtimecatalog.Route{ProviderID: catalogs.ProviderID(route.ProviderID), ProviderModelID: catalogs.ProviderModelID(route.ProviderModelID)})
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			operation := catalogs.ProviderOperationImagesGenerations
			if request.Image.Present() {
				operation = catalogs.ProviderOperationImagesEdits
			}
			valuation, err := runtimecatalog.ImageValuation(offering, operation, time.Now())
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			units, err := runtimecatalog.ImageQuantities(offering.Billing.Images, int64(request.N), request.Size)
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			billing = offering.Billing.Images
			return admission.Quote{Valuation: valuation, Units: units}, nil
		},
		evidence: func(response *connectors.ImagesResponse, err error) *reservation.Evidence {
			if response == nil || err != nil || len(response.Data) == 0 || billing == nil {
				return nil
			}
			for _, image := range response.Data {
				if image.B64JSON == "" && image.URL == "" {
					return nil
				}
			}
			units, err := runtimecatalog.ImageQuantities(billing, int64(len(response.Data)), request.Size)
			if err != nil {
				return nil
			}
			return &reservation.Evidence{Quantities: units}
		},
	}
}
