package router

import (
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

func TestRecognitionBoundEnforcesSelectedOffering(t *testing.T) {
	for _, test := range []struct {
		name  string
		pages int
		media string
		basis catalogs.RecognitionBillingBasis
		want  int64
		valid bool
	}{
		{"tokens", 2, "application/pdf", catalogs.RecognitionBillingTokens, 150, true},
		{"pages", 2, "application/pdf", catalogs.RecognitionBillingPages, 0, true},
		{"too many pages", 3, "application/pdf", catalogs.RecognitionBillingTokens, 0, false},
		{"no pages", 0, "application/pdf", catalogs.RecognitionBillingTokens, 0, false},
		{"other media", 1, "image/png", catalogs.RecognitionBillingTokens, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			billing := &catalogs.RecognitionBilling{Basis: test.basis, RequestCharge: new(false)}
			if test.basis == catalogs.RecognitionBillingTokens {
				billing.Input = []catalogs.TokenBillingClass{catalogs.TokenBillingInput}
				billing.Output = []catalogs.TokenBillingClass{catalogs.TokenBillingOutput}
			}
			offering := catalogs.ProviderOffering{Billing: &catalogs.ModelBilling{Recognition: billing}, Limits: &catalogs.ModelLimits{InputTokens: 100, OutputTokens: 50, DocumentPages: 2}}
			request := &connectors.RecognitionRequest{Document: connectors.UploadedFile{Bytes: []byte("PDF"), MediaType: test.media}, Pages: test.pages}
			bound, err := recognitionBound(offering, request)
			if test.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, admission.ErrBoundUnknown)
			}
			require.Equal(t, test.want, bound)
			if test.valid && test.basis == catalogs.RecognitionBillingTokens {
				require.Equal(t, 50, *request.MaxTokens)
			}
		})
	}
}

func TestRecognitionPageEvidenceRequiresMeasurement(t *testing.T) {
	billing := &catalogs.RecognitionBilling{Basis: catalogs.RecognitionBillingPages, RequestCharge: new(true)}
	for _, test := range []struct {
		name  string
		pages *int
		known bool
	}{
		{"absent", nil, false}, {"negative", new(-1), false}, {"explicit zero", new(0), true}, {"partial", new(2), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := &connectors.RecognitionResponse{ProcessedPages: test.pages, Pages: []connectors.RecognizedPage{{Number: 1}, {Number: 2}, {Number: 3}}}
			evidence := recognitionEvidence(billing, response)
			if !test.known {
				require.Nil(t, evidence)
				return
			}
			require.Equal(t, &reservation.Evidence{Quantities: reservation.Quantities{"page": int64(*test.pages), "request": 1}}, evidence)
		})
	}
}
