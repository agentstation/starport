package proxy

import (
	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPageRecognitionCostRequiresProviderCount(t *testing.T) {
	report := parseReport{}
	report.charge(recognitionPrices(), documentReading{Reading: document.Reading{Offering: "google/gemini-2.5-flash", Pages: 1}})
	require.True(t, report.Unpriced)
	require.Len(t, report.Extractions, 1)
	require.Nil(t, report.Extractions[0].Cost)
	require.Equal(t, usage.CostReasonNoUsage, report.Extractions[0].CostUnavailableReason)
}

func TestPageRecognitionRetainsMeasuredCostAfterFailure(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		report := parseReport{}
		report.charge(recognitionPrices(), documentReading{Reading: document.Reading{Offering: "google/gemini-2.5-flash", Pages: 3}, Failed: true, Usage: &inference.Usage{TokensUnknown: true, ProcessedPages: count, ProcessedPagesKnown: true}})
		require.False(t, report.Unpriced)
		require.EqualValues(t, int64(count)*100000000, report.CostNanoUSD)
		require.True(t, report.Extractions[0].ProcessedPagesKnown)
		require.EqualValues(t, count, report.Extractions[0].ProcessedPages)
		require.EqualValues(t, 3, report.Extractions[0].Pages)
	}
}
