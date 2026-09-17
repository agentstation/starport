package proxy

import (
	"encoding/json"
	"github.com/agentstation/starport/internal/document"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

func tokenRecognitionOffering() catalogs.ProviderOffering {
	return catalogs.ProviderOffering{
		Billing: &catalogs.ModelBilling{Recognition: &catalogs.RecognitionBilling{Basis: catalogs.RecognitionBillingTokens}},
		Pricing: &catalogs.ModelPricing{Currency: "USD", Tokens: &catalogs.ModelTokenPricing{Input: &catalogs.ModelTokenCost{Per1M: 1}, Output: &catalogs.ModelTokenCost{Per1M: 2}, CacheRead: &catalogs.ModelTokenCost{Per1M: 0.5}}},
	}
}

func TestRecognitionCostUsesActualUnits(t *testing.T) {
	measured := &inference.Usage{InputTokens: 100, OutputTokens: 30, TotalTokens: 130, ReasoningTokens: 10, CacheReadTokens: 20}
	offering := tokenRecognitionOffering()
	cost, reason := recognitionCost(offering, 10, measured, time.Now())
	require.Empty(t, reason)
	require.EqualValues(t, 150000, cost.NanoUSD)
	// The same measured work costs the same amount at a different page count.
	again, reason := recognitionCost(offering, 2, measured, time.Now())
	require.Empty(t, reason)
	require.Equal(t, cost, again)
	for _, test := range []struct {
		name   string
		usage  *inference.Usage
		reason string
	}{
		{"missing", nil, usage.CostReasonNoUsage},
		{"estimated", &inference.Usage{InputTokens: 100, TotalTokens: 100, Estimated: true}, usage.CostReasonNoUsage},
		{"negative", &inference.Usage{InputTokens: -1}, usage.CostReasonInvalidUsage},
		{"inconsistent total", &inference.Usage{InputTokens: 100, TotalTokens: 1}, usage.CostReasonInvalidUsage},
		{"inconsistent cache", &inference.Usage{InputTokens: 100, TotalTokens: 100, CacheReadTokens: 101}, usage.CostReasonInvalidUsage},
	} {
		t.Run(test.name, func(t *testing.T) {
			cost, reason := recognitionCost(offering, 2, test.usage, time.Now())
			require.Nil(t, cost)
			require.Equal(t, test.reason, reason)
		})
	}
	offering.Pricing.Currency = "EUR"
	cost, reason = recognitionCost(offering, 2, measured, time.Now())
	require.Nil(t, cost)
	require.Equal(t, usage.CostReasonNoPricing, reason)
}

func TestRecognitionCostUsesContextTierAndKnownZero(t *testing.T) {
	offering := tokenRecognitionOffering()
	offering.Pricing.Tiers = []catalogs.ModelPricingTier{{Type: catalogs.ModelPricingTierTypeContext, Size: 100, Tokens: &catalogs.ModelTokenPricing{Input: &catalogs.ModelTokenCost{Per1M: 2}, Output: &catalogs.ModelTokenCost{Per1M: 4}}}}
	for _, test := range []struct {
		input int
		want  int64
	}{{100, 120000}, {101, 242000}} {
		cost, reason := recognitionCost(offering, 1, &inference.Usage{InputTokens: test.input, OutputTokens: 10, TotalTokens: test.input + 10}, time.Now())
		require.Empty(t, reason)
		require.Equal(t, test.want, cost.NanoUSD)
	}
	offering = tokenRecognitionOffering()
	offering.Pricing.Tokens.Input.SetAmount(catalogs.CostUnitPerMillion, 0)
	offering.Pricing.Tokens.Output.SetAmount(catalogs.CostUnitPerMillion, 0)
	cost, reason := recognitionCost(offering, 1, &inference.Usage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110}, time.Now())
	require.Empty(t, reason)
	require.Zero(t, cost.NanoUSD)
	offering.Pricing.Tokens.Output = nil
	cost, reason = recognitionCost(offering, 1, &inference.Usage{}, time.Now())
	require.Nil(t, cost)
	require.Equal(t, usage.CostReasonNoPricing, reason)
}

func TestRecognitionMeasurementsReachUsageRecord(t *testing.T) {
	prices := recognitionPrices()
	prices.offerings = map[string]catalogs.ProviderOffering{"google/gemini-2.5-flash": tokenRecognitionOffering()}
	service, router, read := meteredProxyWithPrices(t, prices, "Recognized text")
	router.usage = &inference.Usage{InputTokens: 100, OutputTokens: 30, TotalTokens: 130, ReasoningTokens: 10, CacheReadTokens: 20}
	response, err := service.ProcessChatCompletion(catalogContext(t), parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)
	require.EqualValues(t, 150000, response.ExtractionNanoUSD)
	record := read(t)
	require.Len(t, record.Extractions, 1)
	require.Equal(t, "tokens", record.Extractions[0].BillingBasis)
	require.EqualValues(t, 100, record.Extractions[0].Tokens.Input)
	require.EqualValues(t, 30, record.Extractions[0].Tokens.Output)
	require.EqualValues(t, 150000, record.Extractions[0].Cost.NanoUSD)
	response.Extractions[0].Tokens.Input = 1
	require.EqualValues(t, 100, record.Extractions[0].Tokens.Input)
}

func TestRecognitionReasoningRateReclassifiesOutput(t *testing.T) {
	offering := tokenRecognitionOffering()
	offering.Pricing.Tokens.Reasoning = &catalogs.ModelTokenCost{Per1M: 5}
	cost, reason := recognitionCost(offering, 1, &inference.Usage{InputTokens: 100, OutputTokens: 30, ReasoningTokens: 10, TotalTokens: 130}, time.Now())
	require.Empty(t, reason)
	require.EqualValues(t, 190000, cost.NanoUSD)
}

func TestRecognitionPriceValidityUsesCallStart(t *testing.T) {
	offering := tokenRecognitionOffering()
	require.NoError(t, json.Unmarshal([]byte(`{"currency":"USD","effective_until":"2026-01-02T00:00:00Z","tokens":{"input":{"per_1m_tokens":1},"output":{"per_1m_tokens":2}}}`), offering.Pricing))
	prices := recognitionPrices()
	prices.offerings = map[string]catalogs.ProviderOffering{"provider/model": offering}
	started := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	report := parseReport{}
	report.charge(prices, documentReading{Reading: document.Reading{Offering: "provider/model", Pages: 1}, Usage: &inference.Usage{InputTokens: 100, OutputTokens: 30, TotalTokens: 130}, StartedAt: started})
	require.False(t, report.Unpriced)
	require.EqualValues(t, 160000, report.CostNanoUSD)
	require.Equal(t, started, report.Extractions[0].StartedAt)
	cost, reason := recognitionCost(offering, 1, &inference.Usage{InputTokens: 100, OutputTokens: 30, TotalTokens: 130}, started.Add(24*time.Hour))
	require.Nil(t, cost)
	require.Equal(t, usage.CostReasonNoPricing, reason)
}
