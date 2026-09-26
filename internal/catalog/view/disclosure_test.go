package view

import (
	"testing"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/catalog/disclosure"
	"github.com/stretchr/testify/require"
)

func TestViewerProjectionsFilterOfferingFacts(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic", "openai")
	routes := snapshot.RoutesForProvider("anthropic")
	require.NotEmpty(t, routes)
	allowed := routes[0]
	policy := disclosure.New(snapshot, apikey.APIKey{AllowedModels: []string{allowed.ID()}}, account.Account{})
	models := ModelsForViewer(snapshot, policy)
	require.Len(t, models, 1)
	require.Equal(t, string(allowed.DefinitionID), models[0].ID)
	require.Len(t, models[0].Offerings, 1)
	require.Equal(t, string(allowed.ProviderModelID), models[0].Offerings[0].ProviderModelID)
	offering, err := snapshot.Offering(allowed)
	require.NoError(t, err)
	if offering.Pricing != nil && offering.Pricing.Tokens != nil {
		require.Equal(t, formatTokenPrice(offering.Pricing.Tokens.Input), models[0].Pricing.Prompt)
	}
	providers := ProvidersForViewer(snapshot, nil, policy)
	require.Len(t, providers, 1)
	require.Equal(t, []string{allowed.ID()}, providers[0].Models)
	for _, endpoint := range EndpointsForViewer(snapshot, string(allowed.DefinitionID), policy) {
		require.Equal(t, string(allowed.ProviderID), endpoint.Provider)
	}
	authors := AuthorsForViewer(snapshot, policy)
	require.NotEmpty(t, authors)
	for _, author := range authors {
		require.Equal(t, []string{string(allowed.DefinitionID)}, author.Models)
	}
	denied := disclosure.Policy{}
	require.Empty(t, ModelsForViewer(snapshot, denied))
	require.Empty(t, ProvidersForViewer(snapshot, nil, denied))
	require.Empty(t, EndpointsForViewer(snapshot, string(allowed.DefinitionID), denied))
	require.Empty(t, AuthorsForViewer(snapshot, denied))
	_, found := AuthorByIDForViewer(snapshot, authors[0].ID, denied)
	require.False(t, found)
	require.NotEmpty(t, Models(snapshot))
}
