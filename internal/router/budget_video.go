package router

import (
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// videoSubmissionBilling retains one selected offering's billing through dispatch.
type videoSubmissionBilling struct {
	contract   *catalogs.VideoBilling
	valuation  *reservation.Valuation
	quantities reservation.Quantities
	native     bool
}

func (b *videoSubmissionBilling) prepare(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.JobSubmission) error {
	*b = videoSubmissionBilling{native: endpointTypeOf(route) == catalogs.EndpointTypeDeepInfraVideo}
	if !b.native {
		return nil
	}
	if snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID {
		return admission.ErrBoundUnknown
	}
	offering, err := snapshot.Offering(runtimecatalog.Route{ProviderID: catalogs.ProviderID(route.ProviderID), ProviderModelID: catalogs.ProviderModelID(route.ProviderModelID)})
	if err != nil || offering.Billing == nil || offering.Billing.Videos == nil {
		return admission.ErrBoundUnknown
	}
	var seconds int64
	if request.Seconds != "" {
		seconds, err = strconv.ParseInt(request.Seconds, 10, 64)
		if err != nil || seconds <= 0 {
			return admission.ErrBoundUnknown
		}
	}
	b.quantities, err = runtimecatalog.VideoRequestQuantities(offering.Billing.Videos, seconds, request.Size)
	if err != nil {
		return admission.ErrBoundUnknown
	}
	b.contract = offering.Billing.Videos
	request.Seconds = strconv.FormatInt(b.quantities[runtimecatalog.VideoOutputSecondUnit], 10)
	if request.Size == "" {
		request.Size = b.contract.DefaultSize
	}
	valuation, err := runtimecatalog.VideoValuation(offering, time.Now())
	if err == nil {
		b.valuation = &valuation
	}
	return nil
}

func (b *videoSubmissionBilling) charge(*runtimecatalog.RoutableSnapshot, routing.Route, *connectors.JobSubmission) operationCharge[*connectors.ProviderJob] {
	return operationCharge[*connectors.ProviderJob]{
		quote: func(needed admission.Requirements) (admission.Quote, error) {
			if b.valuation == nil || needed.Tokens {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			return admission.Quote{Valuation: *b.valuation, Units: b.quantities}, nil
		},
		evidence: func(answer *connectors.ProviderJob, _ error) *reservation.Evidence { return b.evidence(answer) },
	}
}

func (b *videoSubmissionBilling) evidence(answer *connectors.ProviderJob) *reservation.Evidence {
	if answer == nil || answer.NativeResult == nil || answer.NativeResult.OutputSeconds == nil {
		return nil
	}
	quantities, err := runtimecatalog.VideoMeasuredQuantities(b.contract, *answer.NativeResult.OutputSeconds)
	if err != nil {
		return nil
	}
	return &reservation.Evidence{Quantities: quantities}
}
