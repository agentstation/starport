package view

import (
	"testing"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/disclosure"
	"github.com/stretchr/testify/require"
)

func TestReaderProjectionPreservesPermittedFacts(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic", "openai")
	route := snapshot.RoutesForProvider("anthropic")[0]
	policy := disclosure.New(snapshot, apikey.APIKey{AllowedModels: []string{route.ID()}}, account.Account{Access: []account.ProviderAccess{{Provider: "anthropic"}}})
	summary, ok := SummaryForViewer(runtimecatalog.Summary{GenerationID: snapshot.GenerationID(), Models: 999, Providers: 999}, snapshot, policy)
	require.True(t, ok)
	require.Equal(t, 1, summary.Models)
	require.Equal(t, 1, summary.Providers)
	offering := runtimecatalog.OfferingChange{Provider: string(route.ProviderID), ProviderModelID: string(route.ProviderModelID), DefinitionID: string(route.DefinitionID)}
	price := runtimecatalog.PriceChange{Provider: offering.Provider, ProviderModelID: offering.ProviderModelID, DefinitionID: offering.DefinitionID, Field: "input", PreviousPer1M: 3, CurrentPer1M: 4}
	original := runtimecatalog.Diff{Available: true, ToGenerationID: snapshot.GenerationID(), ModelsAdded: []string{offering.DefinitionID, "denied/new"}, OfferingsAdded: []runtimecatalog.OfferingChange{offering}, OfferingsRemoved: []runtimecatalog.OfferingChange{{Provider: "denied", ProviderModelID: "removed", DefinitionID: "denied/removed"}}, PriceChanges: []runtimecatalog.PriceChange{price}}
	projected, ok := ChangesForViewer(original, snapshot, policy)
	require.True(t, ok)
	require.Equal(t, []string{offering.DefinitionID}, projected.ModelsAdded)
	require.Equal(t, []runtimecatalog.OfferingChange{offering}, projected.OfferingsAdded)
	require.Empty(t, projected.OfferingsRemoved)
	require.Equal(t, []runtimecatalog.PriceChange{price}, projected.PriceChanges)
	require.False(t, projected.SemanticallyEqual)
	projected.ModelsAdded[0] = "caller-mutation"
	projected.PriceChanges[0].CurrentPer1M = 99
	require.Equal(t, offering.DefinitionID, original.ModelsAdded[0])
	require.Equal(t, float64(4), original.PriceChanges[0].CurrentPer1M)
	empty, ok := ChangesForViewer(original, snapshot, disclosure.Policy{})
	require.True(t, ok)
	require.True(t, empty.SemanticallyEqual)
	require.Empty(t, empty.ModelsAdded)
	require.Empty(t, empty.PriceChanges)
}

func TestReaderProjectionRejectsDifferentGeneration(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")
	policy := disclosure.New(snapshot, apikey.APIKey{}, account.Account{})
	summary, ok := SummaryForViewer(runtimecatalog.Summary{GenerationID: "different"}, snapshot, policy)
	require.False(t, ok)
	require.Empty(t, summary)
	diff, ok := ChangesForViewer(runtimecatalog.Diff{Available: true, ToGenerationID: "different", ModelsAdded: []string{"private"}}, snapshot, policy)
	require.False(t, ok)
	require.Empty(t, diff)
}

func TestReaderSummaryMatchesVisibleModelAndProviderLists(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")
	policy := disclosure.New(snapshot, apikey.APIKey{}, account.Account{})
	summary, ok := SummaryForViewer(runtimecatalog.Summary{GenerationID: snapshot.GenerationID()}, snapshot, policy)
	require.True(t, ok)
	require.Equal(t, len(ModelsForViewer(snapshot, policy)), summary.Models)
	require.Equal(t, len(ProvidersForViewer(snapshot, nil, policy)), summary.Providers)
	require.Equal(t, 1, summary.Providers, "unregistered providers must not inflate the compatibility summary")
	visible := make(map[string]bool)
	for _, model := range ModelsForViewer(snapshot, policy) {
		visible[model.ID] = true
	}
	authors := AuthorsForViewer(snapshot, policy)
	require.NotEmpty(t, authors)
	for _, author := range authors {
		detail, found := AuthorByIDForViewer(snapshot, author.ID, policy)
		require.True(t, found)
		require.Equal(t, author, detail)
		for _, id := range author.Models {
			require.True(t, visible[id], "author membership must match the visible model list: %s", id)
		}
	}
}
