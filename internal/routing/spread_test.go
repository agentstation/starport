package routing

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// spreadSnapshot serves one model from four providers. With 100 estimated
// input and output tokens the estimated costs are 300, 330, 360, and 700.
// Under the default ratio of 1.25 the band bound is 375, so the first three
// providers sit inside the band and provider-d sits outside it.
func spreadSnapshot() Snapshot {
	return Snapshot{
		CatalogGenerationID:  "catalog-generation-9",
		AvailabilityRevision: 3,
		Candidates: []Candidate{
			spreadCandidate("provider-a", &TokenCost{InputPerToken: 1, OutputPerToken: 2}),
			spreadCandidate("provider-b", &TokenCost{InputPerToken: 1.1, OutputPerToken: 2.2}),
			spreadCandidate("provider-c", &TokenCost{InputPerToken: 1.2, OutputPerToken: 2.4}),
			spreadCandidate("provider-d", &TokenCost{InputPerToken: 3, OutputPerToken: 4}),
		},
	}
}

func spreadCandidate(provider string, cost *TokenCost) Candidate {
	return Candidate{
		Route: Route{
			CatalogGenerationID: "catalog-generation-9",
			ModelID:             "author/primary",
			ProviderID:          provider,
			ProviderModelID:     "primary",
		},
		ContextWindow: 32_000,
		Cost:          cost,
	}
}

func spreadRequest(seed uint64) Request {
	return Request{
		Models:                []string{"author/primary"},
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
		Optimization: OptimizationPolicy{
			PreferLowestCost:    true,
			PreferLowestLatency: true,
			Spread:              true,
			SpreadSeed:          seed,
		},
	}
}

// TestPlanSpreadDistributesInsideTheBand is the acceptance distribution: over
// 1000 plans every in-band candidate leads at least once, and the out-of-band
// candidate never leads. The out-of-band candidate stays in the plan as the
// last fallback, so spread widens traffic without dropping a route.
func TestPlanSpreadDistributesInsideTheBand(t *testing.T) {
	planner := NewPlanner()
	firstAttempts := make(map[string]int)
	for seed := uint64(0); seed < 1000; seed++ {
		plan, err := planner.Plan(spreadRequest(seed), spreadSnapshot())
		require.NoError(t, err)
		ids := attemptIDs(plan.Attempts())
		require.Len(t, ids, 4)
		require.Equal(t, "provider-d/primary", ids[3], "the out-of-band route stays last")
		firstAttempts[ids[0]]++
	}
	require.Len(t, firstAttempts, 3, "every in-band candidate leads and no other does")
	for _, provider := range []string{"provider-a/primary", "provider-b/primary", "provider-c/primary"} {
		require.Positive(t, firstAttempts[provider], "%s never led the plan", provider)
	}
	require.Greater(t, firstAttempts["provider-a/primary"], firstAttempts["provider-c/primary"],
		"the cheaper route carries more traffic")
}

// TestPlanWithoutSpreadStaysDeterministic holds the default contract: a
// request that does not ask for spread keeps the deterministic plan, byte for
// byte, across repeated runs.
func TestPlanWithoutSpreadStaysDeterministic(t *testing.T) {
	planner := NewPlanner()
	request := spreadRequest(0)
	request.Optimization.Spread = false

	first, err := planner.Plan(request, spreadSnapshot())
	require.NoError(t, err)
	second, err := planner.Plan(request, spreadSnapshot())
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(first, second))
	require.Equal(t, []string{
		"provider-a/primary",
		"provider-b/primary",
		"provider-c/primary",
		"provider-d/primary",
	}, attemptIDs(first.Attempts()))
}

// TestPlanSpreadSameSeedRepeatsThePlan pins the purity contract: the seed is
// part of the request, so the same request plans the same way twice.
func TestPlanSpreadSameSeedRepeatsThePlan(t *testing.T) {
	planner := NewPlanner()
	for seed := uint64(0); seed < 50; seed++ {
		first, err := planner.Plan(spreadRequest(seed), spreadSnapshot())
		require.NoError(t, err)
		second, err := planner.Plan(spreadRequest(seed), spreadSnapshot())
		require.NoError(t, err)
		require.True(t, reflect.DeepEqual(first, second), "seed %d", seed)
	}
}

// TestPlanSpreadBandEndsAtAnUnknownMetric holds the band boundary: a route
// whose cost the catalog does not state cannot join a cost band, so the plan
// keeps the deterministic order for every seed.
func TestPlanSpreadBandEndsAtAnUnknownMetric(t *testing.T) {
	planner := NewPlanner()
	snapshot := Snapshot{
		CatalogGenerationID:  "catalog-generation-9",
		AvailabilityRevision: 3,
		Candidates: []Candidate{
			spreadCandidate("provider-a", &TokenCost{InputPerToken: 1, OutputPerToken: 2}),
			spreadCandidate("provider-b", nil),
		},
	}
	for seed := uint64(0); seed < 50; seed++ {
		plan, err := planner.Plan(spreadRequest(seed), snapshot)
		require.NoError(t, err)
		require.Equal(t, []string{"provider-a/primary", "provider-b/primary"}, attemptIDs(plan.Attempts()))
	}
}

// TestPlanSpreadZeroMetricBandHoldsFreeRoutes holds the free anchor: a zero
// best metric bounds the band at zero, so free routes share traffic equally
// and every priced route stays outside the band.
func TestPlanSpreadZeroMetricBandHoldsFreeRoutes(t *testing.T) {
	planner := NewPlanner()
	snapshot := Snapshot{
		CatalogGenerationID:  "catalog-generation-9",
		AvailabilityRevision: 3,
		Candidates: []Candidate{
			spreadCandidate("provider-a", &TokenCost{}),
			spreadCandidate("provider-b", &TokenCost{}),
			spreadCandidate("provider-c", &TokenCost{InputPerToken: 1, OutputPerToken: 2}),
		},
	}
	firstAttempts := make(map[string]int)
	for seed := uint64(0); seed < 200; seed++ {
		plan, err := planner.Plan(spreadRequest(seed), snapshot)
		require.NoError(t, err)
		ids := attemptIDs(plan.Attempts())
		require.Equal(t, "provider-c/primary", ids[2], "the priced route stays outside the free band")
		firstAttempts[ids[0]]++
	}
	require.Len(t, firstAttempts, 2)
	require.Positive(t, firstAttempts["provider-a/primary"])
	require.Positive(t, firstAttempts["provider-b/primary"])
}
