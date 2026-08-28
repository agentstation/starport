package catalog

import (
	"strings"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// rerankRoutes projects the shipped generation and returns the routes that
// serve reranking. It reuses the media census because the census declares every
// operation each provider's offerings claim, which removes the adapter as a
// variable: a rerank offering that fails to reach a route failed in the
// projection rather than in the availability record.
func rerankRoutes(t *testing.T) (*RoutableSnapshot, []Route) {
	t.Helper()
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)
	adapters, _ := mediaCensus(t, client.Catalog())
	require.NoError(t, plane.ReplaceAdapters(adapters))

	snapshot := plane.Current()
	routes := make([]Route, 0)
	for _, route := range snapshot.Routes() {
		if route.Supports(catalogs.ProviderOperationRerank) {
			routes = append(routes, route)
		}
	}
	return snapshot, routes
}

// TestRerankOfferingsProjectWithTheirPriceAndBound holds the two facts a rerank
// route cannot be planned without. The price is one: a rerank turn that reaches
// the cost seam unpriced reads as free to every spend limit downstream. The
// document bound is the other: a reranker refuses a longer list rather than
// truncating it, so a caller that cannot read the bound sends a request that
// cannot succeed.
//
// The basis decides which price to read. Cohere bills a search unit, which no
// token price converts into, and Voyage AI bills tokens. A consumer that read
// whichever field it found would be right by accident on one provider and
// wrong on the other, so the projection has to carry the basis with the number.
func TestRerankOfferingsProjectWithTheirPriceAndBound(t *testing.T) {
	snapshot, routes := rerankRoutes(t)
	require.NotEmpty(t, routes, "the shipped generation projected no rerank route")

	bases := make(map[catalogs.ModelRerankBasis]int)
	for _, route := range routes {
		endpoint, found := route.Endpoint(catalogs.ProviderOperationRerank)
		require.Truef(t, found, "%s serves rerank with no endpoint", route.ID())
		require.NotEmpty(t, strings.TrimSpace(endpoint.URL))

		offering, err := snapshot.Offering(route)
		require.NoError(t, err)

		require.NotNilf(t, offering.Limits, "%s states no limits", route.ID())
		require.Positivef(t, offering.Limits.MaxDocuments,
			"%s serves rerank and states no document count", route.ID())

		require.NotNilf(t, offering.Pricing, "%s carries no price", route.ID())
		require.NotNilf(t, offering.Pricing.Operations, "%s names no operation price", route.ID())
		basis := offering.Pricing.Operations.RerankBasis
		bases[basis]++
		switch basis {
		case catalogs.ModelRerankBasisSearchUnit:
			require.NotNilf(t, offering.Pricing.Operations.SearchUnit,
				"%s bills a search unit and prices none", route.ID())
			require.Positive(t, *offering.Pricing.Operations.SearchUnit)
		case catalogs.ModelRerankBasisToken:
			require.NotNilf(t, offering.Pricing.Tokens, "%s bills tokens and prices none", route.ID())
			require.NotNil(t, offering.Pricing.Tokens.Input)
			require.Positive(t, offering.Pricing.Tokens.Input.Per1M)
		default:
			t.Fatalf("%s serves rerank and names the basis %q", route.ID(), basis)
		}
	}

	// Both bases have to reach the projection. A generation that carried one of
	// them would let this test pass while the branch that reads the other went
	// unexercised, which is the gap the basis field exists to close.
	require.Positive(t, bases[catalogs.ModelRerankBasisSearchUnit])
	require.Positive(t, bases[catalogs.ModelRerankBasisToken])
}

// TestARerankOfferingServesNoChatOperation is what keeps a reranker out of the
// chat surfaces. A reranker generates nothing: it scores documents and returns
// an order. Every chat surface filters on the operations a route names, so the
// claim to hold is that a rerank route names rerank and nothing a chat surface
// looks for.
func TestARerankOfferingServesNoChatOperation(t *testing.T) {
	_, routes := rerankRoutes(t)
	require.NotEmpty(t, routes)

	for _, route := range routes {
		require.Falsef(t, route.Supports(catalogs.ProviderOperationChatCompletions),
			"%s serves rerank and chat completions", route.ID())
		require.Falsef(t, route.Supports(catalogs.ProviderOperationEmbeddings),
			"%s serves rerank and embeddings", route.ID())
		require.Equal(t,
			[]catalogs.ProviderOperation{catalogs.ProviderOperationRerank},
			route.Operations,
		)
	}

	// The seam that hands a catalog operation to the planner casts the string
	// with no lookup, and the planner treats a name it does not serve as inert.
	// A rename in the catalog would remove reranking from routing with no error
	// anywhere, so the spelling is a contract.
	require.Equal(t, "rerank", string(catalogs.ProviderOperationRerank))
}
