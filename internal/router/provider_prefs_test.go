package router

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

func plannerTestRouter() *modelRouter {
	registry := &mockRegistry{connectors: map[string]connectors.Connector{}}
	return New(registry).(*modelRouter)
}

// routerPlanningSnapshot offers author/primary on a cheap slow provider and
// an expensive fast provider, so cost and latency orderings are distinct.
func routerPlanningSnapshot() routing.Snapshot {
	slowLatency := 100 * time.Millisecond
	fastLatency := 20 * time.Millisecond
	return routing.Snapshot{
		CatalogGenerationID: "router-generation-1",
		Candidates: []routing.Candidate{
			{
				Route: routing.Route{
					CatalogGenerationID: "router-generation-1", ModelID: "author/primary",
					ProviderID: "provider-cheap", ProviderModelID: "primary-cheap",
				},
				Cost:    &routing.TokenCost{InputPerToken: 1, OutputPerToken: 2},
				Latency: &slowLatency,
			},
			{
				Route: routing.Route{
					CatalogGenerationID: "router-generation-1", ModelID: "author/primary",
					ProviderID: "provider-fast", ProviderModelID: "primary-fast",
				},
				Cost:    &routing.TokenCost{InputPerToken: 3, OutputPerToken: 4},
				Latency: &fastLatency,
			},
		},
	}
}

func firstAttemptProvider(t *testing.T, request routing.Request) string {
	t.Helper()
	plan, err := routing.NewPlanner().Plan(request, routerPlanningSnapshot())
	require.NoError(t, err)
	attempts := plan.Attempts()
	require.NotEmpty(t, attempts)
	return attempts[0].Route.ProviderID
}

func TestProviderSortPrice(t *testing.T) {
	router := plannerTestRouter()
	request := router.toPlanningRequest(&Request{
		ChatRequest: &connectors.ChatRequest{Model: "author/primary"},
		ProviderPreferences: &ProviderPreferences{
			Sort:                "price",
			MaxPromptPricePer1M: 2_000_000,
		},
	})
	require.Equal(t, routing.OptimizationPolicy{PreferLowestCost: true}, request.Optimization)
	// Wire prices are USD per million tokens; the planner caps per token.
	require.InDelta(t, 2.0, request.Providers.MaxPromptPricePerToken, 1e-9)

	request.Providers.MaxPromptPricePerToken = 0
	require.Equal(t, "provider-cheap", firstAttemptProvider(t, request))
}

func TestProviderSortLatency(t *testing.T) {
	router := plannerTestRouter()
	request := router.toPlanningRequest(&Request{
		ChatRequest:         &connectors.ChatRequest{Model: "author/primary"},
		ProviderPreferences: &ProviderPreferences{Sort: "latency"},
	})
	require.Equal(t, routing.OptimizationPolicy{PreferLowestLatency: true}, request.Optimization)
	require.Equal(t, "provider-fast", firstAttemptProvider(t, request))

	// Starport measures latency, not throughput: "throughput" routes by latency.
	throughput := router.toPlanningRequest(&Request{
		ChatRequest:         &connectors.ChatRequest{Model: "author/primary"},
		ProviderPreferences: &ProviderPreferences{Sort: "throughput"},
	})
	require.Equal(t, request.Optimization, throughput.Optimization)
}

func TestVariantFloorSortsByPrice(t *testing.T) {
	router := plannerTestRouter()
	request := router.toPlanningRequest(&Request{
		ChatRequest: &connectors.ChatRequest{Model: "author/primary:floor"},
	})
	require.Equal(t, []string{"author/primary"}, request.Models)
	require.Equal(t, routing.OptimizationPolicy{PreferLowestCost: true}, request.Optimization)
	require.Equal(t, "provider-cheap", firstAttemptProvider(t, request))

	// An explicit provider.sort wins over the variant suffix.
	explicit := router.toPlanningRequest(&Request{
		ChatRequest:         &connectors.ChatRequest{Model: "author/primary:floor"},
		ProviderPreferences: &ProviderPreferences{Sort: "latency"},
	})
	require.Equal(t, routing.OptimizationPolicy{PreferLowestLatency: true}, explicit.Optimization)
	require.Equal(t, "provider-fast", firstAttemptProvider(t, explicit))

	// ":nitro" sorts by measured latency.
	nitro := router.toPlanningRequest(&Request{
		ChatRequest: &connectors.ChatRequest{Model: "author/primary:nitro"},
	})
	require.Equal(t, routing.OptimizationPolicy{PreferLowestLatency: true}, nitro.Optimization)

	// An unknown suffix is part of the opaque model ID, not a variant.
	unknown := router.toPlanningRequest(&Request{
		ChatRequest: &connectors.ChatRequest{Model: "author/primary:preview"},
	})
	require.Equal(t, []string{"author/primary:preview"}, unknown.Models)
}

func TestVariantFreeFiltersZeroPrice(t *testing.T) {
	router := plannerTestRouter()
	request := router.toPlanningRequest(&Request{
		ChatRequest: &connectors.ChatRequest{Model: "author/primary:free"},
	})
	require.Equal(t, []string{"author/primary"}, request.Models)
	require.Equal(t, []string{"author/primary"}, request.ZeroPriceModels)

	// Only the zero-price offering survives planning.
	snapshot := routerPlanningSnapshot()
	snapshot.Candidates[0].Cost = &routing.TokenCost{}
	plan, err := routing.NewPlanner().Plan(request, snapshot)
	require.NoError(t, err)
	attempts := plan.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, "provider-cheap", attempts[0].Route.ProviderID)

	// The registry fallback has no price facts, so ":free" fails loudly
	// instead of routing to an offering that may bill the caller.
	_, err = router.planRegistryRoute(context.Background(), &Request{
		ChatRequest: &connectors.ChatRequest{Model: "author/primary:free"},
	}, nil)
	require.ErrorIs(t, err, ErrNoModelsAvailable)
	require.Contains(t, err.Error(), "catalog price facts")
}

func TestOnlyAndIgnoreCompose(t *testing.T) {
	router := plannerTestRouter()
	models := []string{"openai/gpt-4", "anthropic/claude-3", "groq/llama3-70b"}

	// "only" and "ignore" compose instead of "only" silently winning.
	filtered := router.filterByProviderPreferences(models, &ProviderPreferences{
		Only:   []string{"openai", "anthropic"},
		Ignore: []string{"anthropic"},
	})
	require.Equal(t, []string{"openai/gpt-4"}, filtered)

	// Provider names match case-insensitively, as in the catalog planner.
	filtered = router.filterByProviderPreferences(models, &ProviderPreferences{
		Only: []string{"OpenAI"},
	})
	require.Equal(t, []string{"openai/gpt-4"}, filtered)

	// "order" without fallbacks keeps only ordered providers, in rank order.
	filtered = router.filterByProviderPreferences(models, &ProviderPreferences{
		Order:  []string{"groq", "openai"},
		Ignore: []string{"openai"},
	})
	require.Equal(t, []string{"groq/llama3-70b"}, filtered)
}
