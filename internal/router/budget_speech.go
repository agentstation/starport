package router

import (
	"time"
	"unicode/utf8"

	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

func speechCharge(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.SpeechRequest) operationCharge[*connectors.SpeechResponse] {
	var billing *catalogs.SpeechBilling
	return operationCharge[*connectors.SpeechResponse]{
		quote: func(needed admission.Requirements) (admission.Quote, error) {
			if needed.Tokens || snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID || request == nil || request.Input == "" || !utf8.ValidString(request.Input) {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			offering, err := snapshot.Offering(runtimecatalog.Route{ProviderID: catalogs.ProviderID(route.ProviderID), ProviderModelID: catalogs.ProviderModelID(route.ProviderModelID)})
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			valuation, err := runtimecatalog.SpeechValuation(offering, time.Now())
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			count := int64(utf8.RuneCountInString(request.Input))
			if count > offering.Billing.Speech.MaxInputCharacters {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			billing = offering.Billing.Speech
			units := reservation.Quantities{runtimecatalog.SpeechCharacterUnit: count}
			if *billing.RequestCharge {
				units["request"] = 1
			}
			return admission.Quote{Valuation: valuation, Units: units}, nil
		},
		evidence: func(response *connectors.SpeechResponse, err error) *reservation.Evidence {
			if response == nil || err != nil || response.InputCharacters == nil || *response.InputCharacters < 0 || billing == nil {
				return nil
			}
			units := reservation.Quantities{runtimecatalog.SpeechCharacterUnit: *response.InputCharacters}
			if *billing.RequestCharge {
				units["request"] = 1
			}
			return &reservation.Evidence{Quantities: units}
		},
	}
}
