package view

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRecognitionBillingProjectionPreservesUnitsAndEstimate(t *testing.T) {
	require.Nil(t, offeringBilling(nil))
	require.Nil(t, offeringBilling(&catalogs.ModelBilling{}))
	source := &catalogs.ModelBilling{Recognition: &catalogs.RecognitionBilling{
		Basis:             catalogs.RecognitionBillingTokens,
		InputPageEstimate: &catalogs.RecognitionInputPageEstimate{Tokens: 258, Source: "provider documentation", Assumptions: "Standard resolution only"},
	}}
	got := offeringBilling(source)
	require.Equal(t, "tokens", got.Recognition.Basis)
	require.Equal(t, 258.0, got.Recognition.InputPageEstimate.Tokens)
	require.Equal(t, "provider documentation", got.Recognition.InputPageEstimate.Source)
	require.Equal(t, "Standard resolution only", got.Recognition.InputPageEstimate.Assumptions)
	source.Recognition.InputPageEstimate.Tokens = 999
	require.Equal(t, 258.0, got.Recognition.InputPageEstimate.Tokens)
}
