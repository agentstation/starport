package view

import starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

func offeringBilling(billing *starmapcatalogs.ModelBilling) *OfferingBillingInfo {
	if billing == nil || billing.Recognition == nil {
		return nil
	}
	recognition := billing.Recognition
	projected := &RecognitionBillingInfo{Basis: string(recognition.Basis)}
	if estimate := recognition.InputPageEstimate; estimate != nil {
		projected.InputPageEstimate = &RecognitionInputPageEstimateInfo{Tokens: estimate.Tokens, Source: estimate.Source, Assumptions: estimate.Assumptions}
	}
	return &OfferingBillingInfo{Recognition: projected}
}
