package routing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaxPriceRejectsExpensiveRoutes(t *testing.T) {
	planner := NewPlanner()

	// provider-a offers author/primary at {1, 2} per token; provider-b at
	// {3, 4}. A prompt cap of 2 keeps provider-a and rejects provider-b.
	plan, err := planner.Plan(Request{
		Models:    []string{"author/primary"},
		Providers: ProviderPolicy{MaxPromptPricePerToken: 2},
	}, contractSnapshot())
	require.NoError(t, err)
	require.Equal(t, []string{"provider-a/primary-a"}, attemptIDs(plan.Attempts()))
	require.Contains(t, rejectionCodes(plan.Rejections()), RejectionPriceExceeded)

	// A completion cap of 2 also keeps only provider-a (output 2 vs 4).
	plan, err = planner.Plan(Request{
		Models:    []string{"author/primary"},
		Providers: ProviderPolicy{MaxCompletionPricePerToken: 2},
	}, contractSnapshot())
	require.NoError(t, err)
	require.Equal(t, []string{"provider-a/primary-a"}, attemptIDs(plan.Attempts()))

	// A capped request rejects a route whose price is unknown: the cap is a
	// promise the planner can only keep with known prices.
	unknownPrice := contractSnapshot()
	for index := range unknownPrice.Candidates {
		unknownPrice.Candidates[index].Cost = nil
	}
	plan, err = planner.Plan(Request{
		Models:    []string{"author/primary"},
		Providers: ProviderPolicy{MaxPromptPricePerToken: 2},
	}, unknownPrice)
	require.ErrorIs(t, err, ErrNoCandidate)
	for _, rejection := range plan.Rejections() {
		require.Equal(t, RejectionPriceExceeded, rejection.Code)
	}

	// An uncapped request still plans unknown-price routes.
	plan, err = planner.Plan(Request{Models: []string{"author/primary"}}, unknownPrice)
	require.NoError(t, err)
	require.Len(t, plan.Attempts(), 2)

	// A zero-price model constraint (":free") rejects every priced offering.
	plan, err = planner.Plan(Request{
		Models:          []string{"author/primary"},
		ZeroPriceModels: []string{"author/primary"},
	}, contractSnapshot())
	require.ErrorIs(t, err, ErrNoCandidate)
	require.NotEmpty(t, plan.Rejections())
	for _, rejection := range plan.Rejections() {
		require.Equal(t, RejectionPriceExceeded, rejection.Code)
	}

	// A zero-price offering satisfies the ":free" constraint.
	free := contractSnapshot()
	free.Candidates[0].Cost = &TokenCost{}
	plan, err = planner.Plan(Request{
		Models:          []string{"author/primary"},
		ZeroPriceModels: []string{"author/primary"},
	}, free)
	require.NoError(t, err)
	require.Equal(t, []string{"provider-a/primary-a"}, attemptIDs(plan.Attempts()))
}

func TestUnmatchedModelReportsRejection(t *testing.T) {
	planner := NewPlanner()

	// A requested model that matches no catalog offering must produce a
	// model-level rejection, not a bare zero-rejection ErrNoCandidate.
	plan, err := planner.Plan(Request{Models: []string{"author/missing"}}, contractSnapshot())
	require.ErrorIs(t, err, ErrNoCandidate)
	rejections := plan.Rejections()
	require.Len(t, rejections, 1)
	require.Equal(t, RejectionUnknownModel, rejections[0].Code)
	require.Equal(t, "author/missing", rejections[0].Route.ModelID)

	// A variant suffix the caller forgot to strip is an unknown model at this
	// seam (the router strips variants before planning); it must still name
	// the model instead of reporting nothing.
	plan, err = planner.Plan(Request{Models: []string{"author/primary:floor"}}, contractSnapshot())
	require.ErrorIs(t, err, ErrNoCandidate)
	rejections = plan.Rejections()
	require.Len(t, rejections, 1)
	require.Equal(t, RejectionUnknownModel, rejections[0].Code)
	require.Equal(t, "author/primary:floor", rejections[0].Route.ModelID)

	// A matched model that is rejected for real reasons must not also gain an
	// unknown-model rejection.
	unavailable := contractSnapshot()
	for index := range unavailable.Candidates {
		unavailable.Candidates[index].Unavailable = true
	}
	plan, err = planner.Plan(Request{Models: []string{"author/primary"}}, unavailable)
	require.ErrorIs(t, err, ErrNoCandidate)
	for _, rejection := range plan.Rejections() {
		require.NotEqual(t, RejectionUnknownModel, rejection.Code)
	}
	require.NotEmpty(t, plan.Rejections())
}
