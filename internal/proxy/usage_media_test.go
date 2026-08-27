package proxy

import (
	"context"
	"math"
	"testing"

	"github.com/agentstation/starmap"
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/usage"
)

// A media turn produces units no token total describes. A generated image is
// metered per image and a provider reports no tokens for it, so a cost that
// reads tokens alone reports zero for a turn that charged real money. Zero is
// also the number a tenant spend budget subtracts, which makes an unpriced
// media turn unbounded rather than merely unbilled.

// syntheticPricingSource carries one hand-built catalog into the control plane.
type syntheticPricingSource struct{ state starmap.CatalogState }

func (s syntheticPricingSource) CurrentCatalogState() starmap.CatalogState { return s.state }

// mediaPricedSnapshot builds a routable snapshot over one offering whose
// pricing is exactly the argument.
//
// The shipped catalog cannot supply this fact. No offering in it carries an
// audio token price at all, and every offering that prices a generated image
// declares no operation, so none of them resolves to a route. MOD12 closes
// both gaps in Starmap. Until it does, a test that asserts the priced half of
// this rule has to state the price itself rather than read one.
func mediaPricedSnapshot(
	t *testing.T,
	pricing *starmapcatalogs.ModelPricing,
) (*runtimecatalog.RoutableSnapshot, string) {
	t.Helper()
	baselineBuilder, err := starmap.EmbeddedBuilder()
	require.NoError(t, err)
	baseline, err := baselineBuilder.Build()
	require.NoError(t, err)
	builder, err := starmapcatalogs.NewBuilderFrom(baseline)
	require.NoError(t, err)

	provider, err := baseline.Provider(starmapcatalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	source := provider.Models["gpt-4o-2024-08-06"]
	require.NotNil(t, source)
	model := starmapcatalogs.DeepCopyModel(*source)
	model.ID = "media/priced@001"
	model.Name = "Priced Media"
	model.Pricing = pricing
	require.NoError(t, builder.SetProviderModel(starmapcatalogs.ProviderIDOpenAI, model))

	catalog, err := builder.Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(syntheticPricingSource{state: starmap.CatalogState{
		Catalog: catalog, GenerationID: "media-priced", Sequence: 1,
	}})
	require.NoError(t, err)

	offerings, err := catalog.ProviderOfferings(starmapcatalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	var offering starmapcatalogs.ProviderOffering
	for _, candidate := range offerings {
		if candidate.ProviderModelID == "media/priced@001" {
			offering = candidate
			break
		}
	}
	require.NotEmpty(t, offering.ProviderModelID, "the synthetic model produced no offering")

	types := make([]starmapcatalogs.EndpointType, 0, len(offering.Endpoints))
	for _, endpoint := range offering.Endpoints {
		types = append(types, endpoint.Type)
	}
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
		ProviderID:    starmapcatalogs.ProviderIDOpenAI,
		Registered:    true,
		Operations:    append([]starmapcatalogs.ProviderOperation(nil), offering.Service.Operations...),
		EndpointTypes: types,
	}))

	routeID := string(starmapcatalogs.ProviderIDOpenAI) + "/media/priced@001"
	snapshot := plane.Current()
	resolved, found := snapshot.ResolveRoute(routeID)
	require.True(t, found, "the synthetic offering does not route")
	served, err := snapshot.Offering(resolved)
	require.NoError(t, err)
	require.NotNil(t, served.Pricing, "the synthetic pricing did not reach the offering")
	return snapshot, routeID
}

func priceOf(perMillion float64) *starmapcatalogs.ModelTokenCost {
	return &starmapcatalogs.ModelTokenCost{
		PerToken: perMillion / 1e6,
		Per1M:    perMillion,
	}
}

func float(value float64) *float64 { return &value }

// imageAnswerResponse returns a routed answer that carries one generated
// picture beside its words, which is the shape MOD10 taught the adapter to
// build.
func imageAnswerResponse(
	modelID string,
	snapshot *runtimecatalog.RoutableSnapshot,
	promptTokens, completionTokens int,
) *routepkg.Response {
	response := chatEvidenceResponse(modelID, snapshot, promptTokens, completionTokens)
	response.ChatResponse.Choices[0].Message.Images = []connectors.GeneratedImage{{
		Type:     "image_url",
		ImageURL: &connectors.ImageURL{URL: "data:image/png;base64,AAAA"},
	}}
	return response
}

func captureOneRecord(t *testing.T, response *routepkg.Response) usage.Record {
	t.Helper()
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	service := capture.Wrap(&proxy{router: &usageEvidenceRouter{response: response}})

	_, err := service.ProcessChatCompletion(context.Background(), usageChatRequest())
	require.NoError(t, err)
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 1)
	return records[0]
}

// TestAGeneratedImageAgainstATextPriceHasNoCost is the acceptance case. The
// offering here is a real one from the shipped catalog: it prices tokens and
// prices no generated image, which is what every routable offering does today.
// Pricing the token half alone would report a few thousandths of a cent for a
// turn that produced a picture, and that number is not the bill.
func TestAGeneratedImageAgainstATextPriceHasNoCost(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	record := captureOneRecord(t, imageAnswerResponse(routeID, snapshot, 100, 40))

	require.NotNil(t, record.Media, "the record forgot the picture the answer carried")
	require.EqualValues(t, 1, record.Media.GeneratedImages)
	require.Nil(t, record.Cost, "an unpriced picture was billed at the text rate")
	require.Equal(t, usage.CostReasonMediaUnpriced, record.CostUnavailableReason)
	require.NoError(t, record.Validate())
}

// TestAGeneratedImagePricesAtTheImageRate holds the other half of the rule.
// A cost has to appear once the offering names an image price, or the reason
// above would be an excuse rather than a gap.
//
// This one tests the pricing rule directly instead of a routed turn, because
// Starmap v0.9.0 cannot produce the turn. Its offering derivation reads a
// priced image_gen as proof that the model is not a chat model, and it has no
// image operation to give the offering instead, so such an offering carries no
// operation and resolves to no route. That is the same residual the MOD12
// census counts, and MOD12 owns closing it. When it closes, this test grows an
// end-to-end sibling.
func TestAGeneratedImagePricesAtTheImageRate(t *testing.T) {
	pricing := &starmapcatalogs.ModelPricing{
		Currency:   starmapcatalogs.ModelPricingCurrencyUSD,
		Tokens:     &starmapcatalogs.ModelTokenPricing{Input: priceOf(2.50), Output: priceOf(10.00)},
		Operations: &starmapcatalogs.ModelOperationPricing{ImageGen: float(0.04)},
	}
	tokens := usage.Tokens{Input: 1000, Output: 100, Total: 1100}

	media, reason := mediaCost(pricing, tokens, usage.Media{GeneratedImages: 3})
	require.Empty(t, reason)
	require.InDelta(t, 0.12, media, 1e-12)

	// The pictures are twelve cents and the tokens are worth a fraction of one,
	// so a cost that dropped them would be off by two orders of magnitude.
	text, reason := tokenCost(pricing.Tokens, tokens)
	require.Empty(t, reason)
	require.Greater(t, media, text*20)
}

// TestAnImageOnlyAnswerNeedsNoTokenPrice states why the two halves are
// separate. A model that only draws reports no tokens and need not publish a
// token rate, and the earlier rule would have called that offering unpriced.
func TestAnImageOnlyAnswerNeedsNoTokenPrice(t *testing.T) {
	pricing := &starmapcatalogs.ModelPricing{
		Currency:   starmapcatalogs.ModelPricingCurrencyUSD,
		Operations: &starmapcatalogs.ModelOperationPricing{ImageGen: float(0.04)},
	}
	media, reason := mediaCost(pricing, usage.Tokens{}, usage.Media{GeneratedImages: 1})
	require.Empty(t, reason)
	require.InDelta(t, 0.04, media, 1e-12)

	text, reason := tokenCost(pricing.Tokens, usage.Tokens{})
	require.Empty(t, reason)
	require.Zero(t, text)
}

// TestAnAudioTurnAgainstAPricedOfferingCosts holds the audio rule. A provider
// meters audio at its own rate and reports the count as a share of the token
// totals, the way it already reports a cache read. A cost that added the share
// on top would bill the same audio twice, and one that ignored it would bill a
// minute of speech at the price of a paragraph.
func TestAnAudioTurnAgainstAPricedOfferingCosts(t *testing.T) {
	snapshot, routeID := mediaPricedSnapshot(t, &starmapcatalogs.ModelPricing{
		Currency: starmapcatalogs.ModelPricingCurrencyUSD,
		Tokens: &starmapcatalogs.ModelTokenPricing{
			Input:       priceOf(2.50),
			Output:      priceOf(10.00),
			AudioInput:  priceOf(40.00),
			AudioOutput: priceOf(80.00),
		},
	})

	response := chatEvidenceResponse(routeID, snapshot, 1000, 400)
	response.ChatResponse.Usage.PromptTokensDetails = &connectors.PromptTokensDetails{AudioTokens: 600}
	response.ChatResponse.Usage.CompletionTokensDetails = &connectors.CompletionTokensDetails{AudioTokens: 300}
	record := captureOneRecord(t, response)

	require.EqualValues(t, 600, record.Tokens.AudioInput)
	require.EqualValues(t, 300, record.Tokens.AudioOutput)
	require.NotNil(t, record.Cost)

	// 400 plain input, 600 audio input, 100 plain output, 300 audio output.
	// The audio shares come out of the plain totals rather than adding to them.
	expected := int64(math.Round((400*2.50/1e6 + 600*40.00/1e6 + 100*10.00/1e6 + 300*80.00/1e6) * 1e9))
	require.Equal(t, expected, record.Cost.NanoUSD)
}

// TestAnAudioTurnAgainstATextPriceHasNoCost states the gap for audio. Falling
// back to the text rate is not a small error: the offering above prices audio
// input at sixteen times its text input.
func TestAnAudioTurnAgainstATextPriceHasNoCost(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	response := chatEvidenceResponse(routeID, snapshot, 1000, 400)
	response.ChatResponse.Usage.PromptTokensDetails = &connectors.PromptTokensDetails{AudioTokens: 600}
	record := captureOneRecord(t, response)

	require.Nil(t, record.Cost)
	require.Equal(t, usage.CostReasonMediaUnpriced, record.CostUnavailableReason)
}

// TestATextTurnCarriesNoMediaOnItsRecord states the cost of the field. Every
// text turn passes through the same capture, and a media object on all of them
// would change what every stored record reads back as.
func TestATextTurnCarriesNoMediaOnItsRecord(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	record := captureOneRecord(t, chatEvidenceResponse(routeID, snapshot, 100, 40))

	require.Nil(t, record.Media)
	require.NotNil(t, record.Cost)
	require.Empty(t, record.CostUnavailableReason)
}

// TestAStreamedPictureReachesTheRecord holds the streaming half. A streamed
// turn reports its usage on one event and its pictures on others, so no single
// event holds both and the count exists only as a running total.
func TestAStreamedPictureReachesTheRecord(t *testing.T) {
	snapshot, routeID, _ := pricedTestSnapshot(t)
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)

	stream := &evidenceStream{
		model:      routeID,
		provider:   "test-provider",
		credential: "gateway",
		snapshot:   snapshot,
		events: []inference.StreamEvent{
			{Kind: inference.StreamDelta, Model: routeID, ModelUsed: routeID,
				Deltas: []inference.ChoiceDelta{{
					Index: 0,
					Media: []inference.ContentPart{{
						Kind:  inference.ContentImage,
						Image: &inference.Image{URL: "data:image/png;base64,AAAA"},
					}},
				}}},
			{Kind: inference.StreamDelta, Model: routeID, ModelUsed: routeID,
				Deltas: []inference.ChoiceDelta{{Index: 0, Text: "here it is"}}},
			{Kind: inference.StreamUsage, Model: routeID, ModelUsed: routeID,
				Usage: &inference.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		},
	}
	service := capture.Wrap(&proxy{router: &usageEvidenceRouter{stream: stream}})

	request := usageChatRequest()
	request.Request.Stream = true
	got, err := service.ProcessChatCompletionStream(context.Background(), request)
	require.NoError(t, err)
	for {
		_, readErr := got.Read()
		if readErr != nil {
			break
		}
	}
	capture.Flush()

	records := repository.all()
	require.Len(t, records, 1)
	require.NotNil(t, records[0].Media, "the streamed picture never reached the record")
	require.EqualValues(t, 1, records[0].Media.GeneratedImages)
	require.Equal(t, usage.CostReasonMediaUnpriced, records[0].CostUnavailableReason)
}
