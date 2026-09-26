package view

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/disclosure"
	"github.com/stretchr/testify/require"
)

type pricingCatalogSource struct{ state starmap.CatalogState }

func (s pricingCatalogSource) CurrentCatalogState() starmap.CatalogState { return s.state }

func TestCatalogViewsKeepOneOfferingPriceRecordAcrossRefresh(t *testing.T) {
	builder := catalogs.NewEmpty()
	author := catalogs.Author{ID: "author", Name: "Author"}
	require.NoError(t, builder.SetAuthor(author))
	features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "model", Name: "Model", Authors: []catalogs.Author{author}, Features: features}))
	setProvider := func(id catalogs.ProviderID, input float64, output *catalogs.ModelTokenCost) {
		require.NoError(t, builder.SetProvider(catalogs.Provider{ID: id, Name: string(id), Inference: &catalogs.ProviderInference{BaseURL: "https://" + string(id) + ".example", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeOpenAI, Path: "/chat/completions"}}}, Models: map[string]*catalogs.Model{"opaque": {ID: "opaque", ModelRef: "author/model", Features: features, Status: catalogs.ModelStatusActive, Pricing: &catalogs.ModelPricing{Currency: catalogs.ModelPricingCurrencyUSD, Tokens: &catalogs.ModelTokenPricing{Input: &catalogs.ModelTokenCost{Per1M: input}, Output: output}}}}}))
	}
	setProvider("acme", 0, &catalogs.ModelTokenCost{Per1M: 11})
	setProvider("bravo", 7, &catalogs.ModelTokenCost{Per1M: 3})
	first, err := builder.Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(pricingCatalogSource{state: starmap.CatalogState{Catalog: first, GenerationID: "price-one", Sequence: 1}})
	require.NoError(t, err)
	for _, id := range []catalogs.ProviderID{"acme", "bravo"} {
		require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{ProviderID: id, Registered: true, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}}))
	}
	retained := plane.Current()
	check := func(snapshot *runtimecatalog.RoutableSnapshot, prompt, completion string) {
		policy := disclosure.New(snapshot, apikey.APIKey{}, account.Account{})
		models := ModelsForViewer(snapshot, policy)
		require.Len(t, models, 1)
		require.Equal(t, &ModelPricing{Prompt: prompt, Completion: completion, Currency: "USD"}, models[0].Pricing)
		require.Len(t, models[0].Offerings, 2)
		require.Equal(t, &OfferingPricingInfo{Prompt: prompt, Completion: completion, Currency: "USD"}, models[0].Offerings[0].Pricing)
		require.Equal(t, &OfferingPricingInfo{Prompt: "7e-06", Completion: "3e-06", Currency: "USD"}, models[0].Offerings[1].Pricing)
		endpoints := EndpointsForViewer(snapshot, "author/model", policy)
		require.Len(t, endpoints, 2)
		for _, endpoint := range endpoints {
			if endpoint.Provider == "acme" {
				require.Equal(t, prompt, endpoint.CostPrompt)
				require.Equal(t, completion, endpoint.CostOutput)
			} else {
				require.Equal(t, "bravo", endpoint.Provider)
				require.Equal(t, "7e-06", endpoint.CostPrompt)
				require.Equal(t, "3e-06", endpoint.CostOutput)
			}
		}
		summary, ok := SummaryForViewer(runtimecatalog.Summary{GenerationID: snapshot.GenerationID()}, snapshot, policy)
		require.True(t, ok)
		require.Equal(t, len(models), summary.Models)
		require.Equal(t, 2, summary.Providers)
		restricted := disclosure.New(snapshot, apikey.APIKey{}, account.Account{Access: []account.ProviderAccess{{Provider: "bravo"}}})
		permitted := ModelsForViewer(snapshot, restricted)
		require.Len(t, permitted, 1)
		require.Len(t, permitted[0].Offerings, 1)
		require.Equal(t, &ModelPricing{Prompt: "7e-06", Completion: "3e-06", Currency: "USD"}, permitted[0].Pricing)
	}
	check(retained, "0", "1.1e-05")
	setProvider("acme", 2, nil)
	second, err := builder.Build()
	require.NoError(t, err)
	require.NoError(t, plane.Activate(starmap.CatalogState{Catalog: second, GenerationID: "price-two", Sequence: 2}))
	check(plane.Current(), "2e-06", "")
	check(retained, "0", "1.1e-05")
}
