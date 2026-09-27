package router

import (
	"math"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

func recognitionCharge(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.RecognitionRequest) operationCharge[*connectors.RecognitionResponse] {
	var billing *catalogs.RecognitionBilling
	return operationCharge[*connectors.RecognitionResponse]{
		quote: func(needed admission.Requirements) (admission.Quote, error) {
			if snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			offering, err := snapshot.Offering(runtimecatalog.Route{ProviderID: catalogs.ProviderID(route.ProviderID), ProviderModelID: catalogs.ProviderModelID(route.ProviderModelID)})
			if err != nil || offering.Billing == nil || offering.Billing.Recognition == nil || offering.Billing.Validate() != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			billing = offering.Billing.Recognition
			if needed.Tokens && billing.Basis == catalogs.RecognitionBillingPages {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			if !needed.Spend {
				copied := *billing
				copied.RequestCharge = nil
				billing = &copied
			}
			bound, err := recognitionBound(offering, request)
			if err != nil {
				return admission.Quote{}, err
			}
			quote := admission.Quote{}
			quote.Tokens = &bound
			if needed.Spend {
				quote.Valuation, err = runtimecatalog.RecognitionValuation(offering, time.Now())
				if err != nil {
					return admission.Quote{}, admission.ErrBoundUnknown
				}
				quote.Units = reservation.Quantities{}
				if billing.Basis == catalogs.RecognitionBillingPages {
					quote.Units["page"] = int64(request.Pages)
				} else {
					input, _ := offering.Limits.Value(catalogs.ModelLimitInputTokens)
					if input <= 0 {
						input, _ = offering.Limits.Value(catalogs.ModelLimitContextWindow)
					}
					output, _ := offering.Limits.Value(catalogs.ModelLimitOutputTokens)
					for _, class := range billing.Input {
						quote.Units[string(class)] = input
					}
					for _, class := range billing.Output {
						quote.Units[string(class)] = output
					}
				}
				if *billing.RequestCharge {
					quote.Units["request"] = 1
				}
			}
			return quote, nil
		},
		evidence: func(response *connectors.RecognitionResponse, _ error) *reservation.Evidence {
			return recognitionEvidence(billing, response)
		},
	}
}

func recognitionBound(offering catalogs.ProviderOffering, request *connectors.RecognitionRequest) (int64, error) {
	if request == nil || request.Pages <= 0 || !request.Document.Present() || request.Document.MediaType != "application/pdf" || offering.Billing == nil || offering.Billing.Recognition == nil || offering.Billing.Validate() != nil {
		return 0, admission.ErrBoundUnknown
	}
	if pages, known := offering.Limits.Value(catalogs.ModelLimitDocumentPages); known == catalogs.ValueKnown && int64(request.Pages) > pages {
		return 0, admission.ErrBoundUnknown
	}
	switch offering.Billing.Recognition.Basis {
	case catalogs.RecognitionBillingTokens:
		bound, err := declaredChatTokenBound(offering.Limits, 1)
		if err != nil {
			return 0, err
		}
		output, _ := offering.Limits.Value(catalogs.ModelLimitOutputTokens)
		if output > math.MaxInt {
			return 0, admission.ErrBoundUnknown
		}
		request.MaxTokens = new(int(output))
		return bound, nil
	case catalogs.RecognitionBillingPages:
		if offering.Billing.Recognition.RequestCharge != nil {
			return 0, nil
		}
	}
	return 0, admission.ErrBoundUnknown
}

func recognitionEvidence(billing *catalogs.RecognitionBilling, response *connectors.RecognitionResponse) *reservation.Evidence {
	if response == nil || billing == nil {
		return nil
	}
	if billing.Basis == catalogs.RecognitionBillingPages {
		if billing.RequestCharge == nil || response.ProcessedPages == nil || *response.ProcessedPages < 0 {
			return nil
		}
		units := reservation.Quantities{"page": int64(*response.ProcessedPages)}
		if *billing.RequestCharge {
			units["request"] = 1
		}
		return &reservation.Evidence{Quantities: units}
	}
	u := response.TokenEvidence
	if u == nil || !u.HasReportedTotals() || u.PromptTokens < 0 || u.CompletionTokens < 0 || u.PromptTokens > math.MaxInt-u.CompletionTokens || u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		return nil
	}
	result := &reservation.Evidence{Tokens: int64(u.TotalTokens)}
	if billing.RequestCharge != nil {
		units, known := textChatQuantities(&catalogs.TextChatBilling{Input: billing.Input, Output: billing.Output, RequestCharge: billing.RequestCharge}, u)
		if !known {
			return nil
		}
		result.Quantities = units
	}
	return result
}
