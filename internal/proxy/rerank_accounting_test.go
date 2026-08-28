package proxy

import (
	"context"
	"testing"

	"github.com/agentstation/starmap"
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/usage"
)

// A rerank turn produces no completion tokens, and on Cohere it produces no
// tokens at all. A meter that read tokens alone would record every such turn as
// free, so the search unit is the only thing between reranking and an account
// that is never billed for it.

// rerankRouter answers the rerank route with the units a test names, and counts
// what reached it. The count is the assertion a spend refusal depends on:
// refusing after the call would report the same message and would already have
// paid the provider.
type rerankRouter struct {
	*capturingRouter
	snapshot *runtimecatalog.RoutableSnapshot
	routeID  string
	units    int
	calls    int
}

func (r *rerankRouter) RouteRerank(
	_ context.Context,
	req *routepkg.RerankRequest,
) (*routepkg.RerankResponse, error) {
	r.calls++
	return &routepkg.RerankResponse{
		Response: inference.RerankResponse{
			Model:   req.Request.Model,
			Results: []inference.RerankResult{{Index: 0, RelevanceScore: 0.9}},
			Usage:   inference.Usage{SearchUnits: r.units},
		},
		ModelUsed:       r.routeID,
		ProviderUsed:    "cohere",
		Attempts:        1,
		CatalogSnapshot: r.snapshot,
	}, nil
}

// rerankRoutes projects the shipped catalog with every rerank provider's
// adapter registered, and names one route of each billing basis.
//
// Both bases are needed. The search unit route is what the meter prices, and
// the token route is the offering that publishes no search unit price at all,
// which is the only way to reach the unpriced reason without a hand-built
// snapshot that could drift from the real projection.
type rerankRoutes struct {
	snapshot   *runtimecatalog.RoutableSnapshot
	searchUnit string
	unitPrice  float64
	tokenBased string
}

func shippedRerankRoutes(t *testing.T) rerankRoutes {
	t.Helper()
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	found := rerankRoutes{}
	for _, provider := range client.Catalog().Providers().List() {
		offerings, err := client.Catalog().ProviderOfferings(provider.ID)
		require.NoError(t, err)
		for _, offering := range offerings {
			if !offering.Supports(starmapcatalogs.ProviderOperationRerank) {
				continue
			}
			types := make([]starmapcatalogs.EndpointType, 0, len(offering.Endpoints))
			for _, endpoint := range offering.Endpoints {
				types = append(types, endpoint.Type)
			}
			require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
				ProviderID:    provider.ID,
				Registered:    true,
				Operations:    append([]starmapcatalogs.ProviderOperation(nil), offering.Service.Operations...),
				EndpointTypes: types,
			}))
			routeID := string(provider.ID) + "/" + string(offering.ProviderModelID)
			switch offering.Pricing.Operations.RerankBasis {
			case starmapcatalogs.ModelRerankBasisSearchUnit:
				if found.searchUnit == "" {
					found.searchUnit = routeID
					found.unitPrice = *offering.Pricing.Operations.SearchUnit
				}
			case starmapcatalogs.ModelRerankBasisToken:
				if found.tokenBased == "" {
					found.tokenBased = routeID
				}
			}
		}
	}
	require.NotEmpty(t, found.searchUnit, "the shipped catalog bills no rerank offering by search unit")
	require.NotEmpty(t, found.tokenBased, "the shipped catalog bills no rerank offering by token")

	found.snapshot = plane.Current()
	for _, routeID := range []string{found.searchUnit, found.tokenBased} {
		_, routable := found.snapshot.ResolveRoute(routeID)
		require.Truef(t, routable, "%s is not routable", routeID)
	}
	return found
}

func rerankTurn(model string) *RerankRequest {
	return &RerankRequest{
		Request: inference.RerankRequest{
			Model:     model,
			Query:     "which provider serves reranking",
			Documents: []string{"a poem about the sea", "Cohere serves reranking"},
		},
		TenantID:  "acme",
		KeyID:     "key-1",
		RequestID: "req-1",
		Protocol:  "openrouter",
	}
}

// TestARerankTurnRecordsItsSearchUnitsAndItsCost is the fail-before RNK8 names.
// Before this, a rerank record carried zero of everything the meter reads, so
// the account was billed nothing for work the provider charged for.
func TestARerankTurnRecordsItsSearchUnitsAndItsCost(t *testing.T) {
	routes := shippedRerankRoutes(t)
	require.Positive(t, routes.unitPrice, "the fixture offering is free, so it proves no arithmetic")

	router := &rerankRouter{
		capturingRouter: &capturingRouter{},
		snapshot:        routes.snapshot,
		routeID:         routes.searchUnit,
		// Two units rather than one. A cost that multiplied by the wrong count
		// would still be nonzero at a single unit.
		units: 2,
	}
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	service := capture.Wrap(&proxy{router: router})

	response, err := service.ProcessRerank(context.Background(), rerankTurn(routes.searchUnit))
	require.NoError(t, err)

	capture.Flush()
	records := repository.all()
	require.Len(t, records, 1)
	record := records[0]

	require.Equal(t, usage.OperationRerank, record.Operation)
	require.Equal(t, int64(2), record.SearchUnits)
	require.Empty(t, record.CostUnavailableReason)
	require.NotNil(t, record.Cost)
	require.Equal(t, nanoUSD(2*routes.unitPrice), record.Cost.NanoUSD)
	require.Positive(t, record.Cost.NanoUSD)

	// The figure the caller reads and the figure the account is billed are the
	// same object, so the two cannot drift apart.
	require.Equal(t, record.Cost, response.Cost)
}

// TestASearchUnitOnAnUnpricedOfferingIsNeverBilledAtZero holds decision RNK-D7
// at the meter. The catalog projection drops an offering that publishes no
// price in the unit it bills, so reaching this state means a snapshot got past
// that guard. Recording zero would report the turn as free and let the account
// spend without limit.
func TestASearchUnitOnAnUnpricedOfferingIsNeverBilledAtZero(t *testing.T) {
	routes := shippedRerankRoutes(t)

	// A route that bills the tokens it reads publishes no search unit price, so
	// a turn reporting one on it is exactly the mismatch this reason names.
	cost, reason := usageCost(routes.snapshot, usage.Record{
		Operation:   usage.OperationRerank,
		ModelUsed:   routes.tokenBased,
		SearchUnits: 3,
	})
	require.Nil(t, cost)
	require.Equal(t, usage.CostReasonRerankUnpriced, reason)

	// The same turn on the offering that does publish the price is billed, so
	// the reason above names the offering rather than the operation.
	cost, reason = usageCost(routes.snapshot, usage.Record{
		Operation:   usage.OperationRerank,
		ModelUsed:   routes.searchUnit,
		SearchUnits: 3,
	})
	require.Empty(t, reason)
	require.NotNil(t, cost)
	require.Equal(t, nanoUSD(3*routes.unitPrice), cost.NanoUSD)
}

// TestATenantAtItsSpendBoundIsRefusedBeforeTheProviderCall is the second half
// of the budget. The door refuses a window already spent, which lets the
// crossing request through; here the floor price is known before the money is
// spent, so the refusal costs the account nothing.
func TestATenantAtItsSpendBoundIsRefusedBeforeTheProviderCall(t *testing.T) {
	const model = "cohere/rerank-v3.5"
	router := &rerankRouter{capturingRouter: &capturingRouter{}, units: 1}
	service := &proxy{
		router: router,
		prices: &priceFixture{searchUnit: map[string]float64{model: 0.0025}},
	}

	// One unit at this model's cheapest offering costs 2,500,000 nano-USD, and
	// the account has less than that left in its window.
	spent := limits.ContextWithAllowance(
		context.Background(),
		limits.Allowance{NanoUSD: 2_499_999, Bounded: true},
	)
	_, err := service.ProcessRerank(spent, rerankTurn(model))
	require.ErrorIs(t, err, limits.ErrSpendLimitExceeded)
	require.Zero(t, router.calls, "the provider was paid for a call the account could not afford")

	var refusal *failure.Failure
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, failure.Billing, refusal.Kind())
	require.Contains(t, refusal.SafeMessage(), model)

	// One nano-USD more and the same call goes through, which is what keeps the
	// refusal a bound rather than a block on reranking.
	affordable := limits.ContextWithAllowance(
		context.Background(),
		limits.Allowance{NanoUSD: 2_500_000, Bounded: true},
	)
	_, err = service.ProcessRerank(affordable, rerankTurn(model))
	require.NoError(t, err)
	require.Equal(t, 1, router.calls)
}

// TestAnUnboundedAccountAndATokenBilledModelRefuseNothing states where the
// estimate stops. Most deployments set no spend budget at all, and a provider
// that bills the tokens it reads has read nothing yet, so neither case has a
// floor to refuse against.
func TestAnUnboundedAccountAndATokenBilledModelRefuseNothing(t *testing.T) {
	router := &rerankRouter{capturingRouter: &capturingRouter{}, units: 1}
	service := &proxy{
		router: router,
		prices: &priceFixture{searchUnit: map[string]float64{"cohere/rerank-v3.5": 0.0025}},
	}

	_, err := service.ProcessRerank(context.Background(), rerankTurn("cohere/rerank-v3.5"))
	require.NoError(t, err)

	// The same account, at the same empty allowance, on a model that publishes
	// no search unit price.
	spent := limits.ContextWithAllowance(
		context.Background(),
		limits.Allowance{NanoUSD: 0, Bounded: true},
	)
	_, err = service.ProcessRerank(spent, rerankTurn("voyage-ai/rerank-2.5"))
	require.NoError(t, err)
	require.Equal(t, 2, router.calls)
}
