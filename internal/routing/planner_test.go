package routing

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRoutePlannerContract(t *testing.T) {
	t.Run("model and provider order with fallback", func(t *testing.T) {
		planner := NewPlanner()
		snapshot := contractSnapshot()
		request := Request{
			Models:              []string{"author/primary", "author/fallback"},
			AllowModelFallbacks: true,
			Providers: ProviderPolicy{
				Order:          []string{"provider-b", "provider-a"},
				AllowFallbacks: true,
			},
		}

		plan, err := planner.Plan(request, snapshot)
		require.NoError(t, err)
		require.Equal(t, []string{
			"provider-b/primary-b",
			"provider-a/primary-a",
			"provider-b/fallback-b",
		}, attemptIDs(plan.Attempts()))
		require.Equal(t, snapshot.CatalogGenerationID, plan.CatalogGenerationID())
		require.Equal(t, snapshot.AvailabilityRevision, plan.AvailabilityRevision())

		request.AllowModelFallbacks = false
		plan, err = planner.Plan(request, snapshot)
		require.NoError(t, err)
		require.Equal(t, []string{
			"provider-b/primary-b",
			"provider-a/primary-a",
		}, attemptIDs(plan.Attempts()))
	})

	t.Run("capability and context requirements", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models:                []string{"author/primary"},
			RequiredCapabilities:  []string{"tools", "vision"},
			RequiredContextTokens: 64_000,
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{"provider-b/primary-b"}, attemptIDs(plan.Attempts()))
		require.Contains(t, rejectionCodes(plan.Rejections()), RejectionMissingCapability)
	})

	t.Run("runtime availability and health", func(t *testing.T) {
		planner := NewPlanner()
		snapshot := contractSnapshot()
		snapshot.Candidates[0].Unavailable = true
		snapshot.Candidates[1].Unhealthy = true
		plan, err := planner.Plan(Request{Models: []string{"author/primary"}}, snapshot)
		require.ErrorIs(t, err, ErrNoCandidate)
		require.ElementsMatch(t, []RejectionCode{
			RejectionUnavailable,
			RejectionUnhealthy,
		}, rejectionCodes(plan.Rejections()))
	})

	t.Run("account and provider policy", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models: []string{"author/primary"},
			Account: AccountPolicy{
				AllowedModels:    []string{"author/primary"},
				AllowedProviders: []string{"provider-b"},
			},
			Providers: ProviderPolicy{
				Only: []string{"provider-a", "provider-b"},
			},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{"provider-b/primary-b"}, attemptIDs(plan.Attempts()))
		require.Contains(t, rejectionCodes(plan.Rejections()), RejectionAccountProvider)
	})

	t.Run("account model override", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models: []string{"account/default"},
			Account: AccountPolicy{
				AllowedModels:  []string{"account/default"},
				ModelOverrides: map[string]string{"account/default": "author/fallback"},
			},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{"provider-b/fallback-b"}, attemptIDs(plan.Attempts()))
	})

	t.Run("provider order can prohibit unlisted fallbacks", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models: []string{"author/primary"},
			Providers: ProviderPolicy{
				Order:          []string{"provider-a"},
				AllowFallbacks: false,
			},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{"provider-a/primary-a"}, attemptIDs(plan.Attempts()))
		require.Contains(t, rejectionCodes(plan.Rejections()), RejectionProviderPolicy)
	})

	t.Run("cost then latency optimization", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models: []string{"author/primary"},
			Optimization: OptimizationPolicy{
				PreferLowestCost:    true,
				PreferLowestLatency: true,
			},
			EstimatedInputTokens:  1_000,
			EstimatedOutputTokens: 500,
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{
			"provider-a/primary-a",
			"provider-b/primary-b",
		}, attemptIDs(plan.Attempts()))
	})

	t.Run("explicit model precedes any-model fallback", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models:                []string{"author/fallback"},
			AllowAnyModelFallback: true,
			Optimization: OptimizationPolicy{
				PreferLowestCost: true,
			},
			EstimatedInputTokens:  1_000,
			EstimatedOutputTokens: 500,
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{
			"provider-b/fallback-b",
			"provider-a/primary-a",
			"provider-b/primary-b",
		}, attemptIDs(plan.Attempts()))
	})

	t.Run("latency optimization", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models: []string{"author/primary"},
			Optimization: OptimizationPolicy{
				PreferLowestLatency: true,
			},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{
			"provider-b/primary-b",
			"provider-a/primary-a",
		}, attemptIDs(plan.Attempts()))
	})

	t.Run("affinity applies after explicit provider order", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models:           []string{"author/primary"},
			AffinityProvider: "provider-b",
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, "provider-b/primary-b", plan.Attempts()[0].Route.ID())

		plan, err = planner.Plan(Request{
			Models:           []string{"author/primary"},
			AffinityProvider: "provider-b",
			Providers: ProviderPolicy{
				Order:          []string{"provider-a", "provider-b"},
				AllowFallbacks: true,
			},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, "provider-a/primary-a", plan.Attempts()[0].Route.ID())
	})

	t.Run("no candidate retains rejection evidence", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models:               []string{"author/primary"},
			RequiredCapabilities: []string{"audio"},
		}, contractSnapshot())
		require.ErrorIs(t, err, ErrNoCandidate)
		require.NotNil(t, plan)
		require.Empty(t, plan.Attempts())
		require.Len(t, plan.Rejections(), 2)
	})
}

func TestRoutePlannerDeterministic(t *testing.T) {
	planner := NewPlanner()
	request := Request{
		Models:              []string{"author/primary", "author/fallback"},
		AllowModelFallbacks: true,
		Optimization: OptimizationPolicy{
			PreferLowestCost:    true,
			PreferLowestLatency: true,
		},
		EstimatedInputTokens:  1_000,
		EstimatedOutputTokens: 500,
	}
	snapshot := contractSnapshot()
	want, err := planner.Plan(request, snapshot)
	require.NoError(t, err)

	for iteration := 0; iteration < 100; iteration++ {
		candidates := append([]Candidate(nil), snapshot.Candidates...)
		for left, right := 0, len(candidates)-1; left < right; left, right = left+1, right-1 {
			candidates[left], candidates[right] = candidates[right], candidates[left]
		}
		snapshot.Candidates = candidates
		got, err := planner.Plan(request, snapshot)
		require.NoError(t, err)
		require.Equal(t, want.Attempts(), got.Attempts())
		require.Equal(t, want.Rejections(), got.Rejections())
	}
}

func TestEmbeddingRequiresCatalogAndAdapterCapability(t *testing.T) {
	const generation = "catalog-generation-embedding"
	candidate := Candidate{
		Route: Route{
			CatalogGenerationID: generation,
			ModelID:             "author/embedding-model",
			ProviderID:          "provider",
			ProviderModelID:     "opaque/embedding@001",
		},
		Operations: []Operation{OperationChatCompletions},
		Endpoints: map[Operation]Endpoint{
			OperationChatCompletions: {Protocol: "openai", URL: "https://provider.test/chat"},
		},
	}
	request := Request{
		Models:    []string{"author/embedding-model"},
		Operation: OperationEmbeddings,
	}

	plan, err := NewPlanner().Plan(request, Snapshot{
		CatalogGenerationID: generation,
		Candidates:          []Candidate{candidate},
	})
	require.ErrorIs(t, err, ErrNoCandidate)
	require.Equal(t, []RejectionCode{RejectionMissingOperation}, rejectionCodes(plan.Rejections()))

	// Operations in a candidate are already the Starmap offering and compiled
	// adapter intersection. The operation becomes eligible only when that
	// intersection also has an exact endpoint.
	candidate.Operations = append(candidate.Operations, OperationEmbeddings)
	candidate.Endpoints[OperationEmbeddings] = Endpoint{
		Protocol: "openai",
		URL:      "https://provider.test/embeddings",
	}
	plan, err = NewPlanner().Plan(request, Snapshot{
		CatalogGenerationID: generation,
		Candidates:          []Candidate{candidate},
	})
	require.NoError(t, err)
	require.Equal(t, OperationEmbeddings, plan.Attempts()[0].Route.Operation)
	require.Equal(t, candidate.Endpoints[OperationEmbeddings], plan.Attempts()[0].Route.Endpoint)
}

func FuzzRoutePlanner(f *testing.F) {
	f.Add("provider-a", "author/primary", true, true, uint16(1_000))
	planner := NewPlanner()
	f.Fuzz(func(t *testing.T, provider, model string, available, healthy bool, tokens uint16) {
		if provider == "" || model == "" {
			t.Skip()
		}
		snapshot := Snapshot{
			CatalogGenerationID: "fuzz-generation",
			Candidates: []Candidate{{
				Route: Route{
					CatalogGenerationID: "fuzz-generation",
					ModelID:             model,
					ProviderID:          provider,
					ProviderModelID:     "provider-model",
				},
				Unavailable: !available,
				Unhealthy:   !healthy,
			}},
		}
		request := Request{Models: []string{model}, EstimatedInputTokens: int(tokens)}
		first, firstErr := planner.Plan(request, snapshot)
		second, secondErr := planner.Plan(request, snapshot)
		require.Equal(t, errors.Is(firstErr, ErrNoCandidate), errors.Is(secondErr, ErrNoCandidate))
		require.Equal(t, first.Attempts(), second.Attempts())
		require.Equal(t, first.Rejections(), second.Rejections())
	})
}

func BenchmarkRoutePlanner(b *testing.B) {
	planner := NewPlanner()
	request := Request{
		Models:              []string{"author/primary", "author/fallback"},
		AllowModelFallbacks: true,
		Optimization: OptimizationPolicy{
			PreferLowestCost:    true,
			PreferLowestLatency: true,
		},
		EstimatedInputTokens:  1_000,
		EstimatedOutputTokens: 500,
	}
	snapshot := contractSnapshot()
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := planner.Plan(request, snapshot); err != nil {
			b.Fatal(err)
		}
	}
}

func contractSnapshot() Snapshot {
	latencyA := 100 * time.Millisecond
	latencyB := 20 * time.Millisecond
	return Snapshot{
		CatalogGenerationID:  "catalog-generation-7",
		AvailabilityRevision: 11,
		Candidates: []Candidate{
			{
				Route: Route{
					CatalogGenerationID: "catalog-generation-7",
					ModelID:             "author/primary",
					ProviderID:          "provider-a",
					ProviderModelID:     "primary-a",
				},
				Capabilities:  []string{"tools"},
				ContextWindow: 32_000,
				Cost:          &TokenCost{InputPerToken: 1, OutputPerToken: 2},
				Latency:       &latencyA,
			},
			{
				Route: Route{
					CatalogGenerationID: "catalog-generation-7",
					ModelID:             "author/primary",
					ProviderID:          "provider-b",
					ProviderModelID:     "primary-b",
				},
				Capabilities:  []string{"tools", "vision"},
				ContextWindow: 128_000,
				Cost:          &TokenCost{InputPerToken: 3, OutputPerToken: 4},
				Latency:       &latencyB,
			},
			{
				Route: Route{
					CatalogGenerationID: "catalog-generation-7",
					ModelID:             "author/fallback",
					ProviderID:          "provider-b",
					ProviderModelID:     "fallback-b",
				},
				Capabilities:  []string{"tools", "vision"},
				ContextWindow: 128_000,
				Cost:          &TokenCost{InputPerToken: 2, OutputPerToken: 3},
				Latency:       &latencyB,
			},
		},
	}
}

func attemptIDs(attempts []Attempt) []string {
	ids := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		ids = append(ids, attempt.Route.ID())
	}
	return ids
}

func rejectionCodes(rejections []Rejection) []RejectionCode {
	codes := make([]RejectionCode, 0, len(rejections))
	for _, rejection := range rejections {
		codes = append(codes, rejection.Code)
	}
	return codes
}

// TestAccountProviderAccess proves the paired grant that a flat set cannot
// express: one provider open to every model while another is narrowed to
// specific models, without the narrow entry denying the open provider.
func TestAccountProviderAccess(t *testing.T) {
	t.Run("a narrowed provider does not deny an open one", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models:              []string{"author/primary", "author/fallback"},
			AllowModelFallbacks: true,
			Account: AccountPolicy{
				Access: []ProviderAccess{
					{Provider: "provider-a"},
					{Provider: "provider-b", Models: []string{"author/fallback"}},
				},
			},
			Providers: ProviderPolicy{AllowFallbacks: true},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{
			"provider-a/primary-a",
			"provider-b/fallback-b",
		}, attemptIDs(plan.Attempts()))
		require.Contains(t, rejectionCodes(plan.Rejections()), RejectionAccountModel)
	})

	t.Run("an unlisted provider is refused", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models: []string{"author/primary"},
			Account: AccountPolicy{
				Access: []ProviderAccess{{Provider: "provider-b"}},
			},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{"provider-b/primary-b"}, attemptIDs(plan.Attempts()))
		require.Contains(t, rejectionCodes(plan.Rejections()), RejectionAccountProvider)
	})

	t.Run("no access entries grants every provider and model", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models:    []string{"author/primary"},
			Providers: ProviderPolicy{AllowFallbacks: true},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Len(t, attemptIDs(plan.Attempts()), 2)
	})

	t.Run("access composes with the flat allow lists", func(t *testing.T) {
		planner := NewPlanner()
		plan, err := planner.Plan(Request{
			Models: []string{"author/primary"},
			Account: AccountPolicy{
				AllowedProviders: []string{"provider-a"},
				Access:           []ProviderAccess{{Provider: "provider-a"}, {Provider: "provider-b"}},
			},
		}, contractSnapshot())
		require.NoError(t, err)
		require.Equal(t, []string{"provider-a/primary-a"}, attemptIDs(plan.Attempts()))
	})
}
