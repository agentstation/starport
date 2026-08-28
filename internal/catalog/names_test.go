package catalog

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/stretchr/testify/require"
)

// TestNamesHoldsOfferingsThePlannerExcluded pins the one property that makes
// Names worth having beside ResolveRoute. A gateway answers "no such model" for
// a name the catalog does not hold and "try again" for a name it holds and
// cannot reach today. Those two answers send an operator to different places,
// so the lookup behind the first one has to read the whole generation.
//
// The contrast below is the test: one name that the planner excluded answers
// true here and false through ResolveRoute.
func TestNamesHoldsOfferingsThePlannerExcluded(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)

	// No adapter is registered, so the planner keeps nothing and every offering
	// in the generation carries the adapter_not_ready exclusion. That is the
	// state a fresh gateway runs in before it configures a provider.
	snapshot := plane.Current()
	require.Empty(t, snapshot.Routes())

	verdicts := snapshot.OfferingRoutability()
	require.NotEmpty(t, verdicts)
	excluded := verdicts[0]
	require.False(t, excluded.Routable)
	name := string(excluded.ProviderID) + "/" + string(excluded.ProviderModelID)

	require.True(t, snapshot.Names(name),
		"%s is in the generation and Names refused it", name)
	_, routable := snapshot.ResolveRoute(name)
	require.False(t, routable,
		"%s has no adapter and ResolveRoute accepted it", name)
}

// TestNamesRefusesAModelTheGenerationNeverHeld covers the other direction. A
// lookup that answered true for everything would let a misspelled model reach a
// provider as a paid error.
func TestNamesRefusesAModelTheGenerationNeverHeld(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)
	snapshot := plane.Current()

	require.False(t, snapshot.Names("no-such-provider/no-such-model"))
	require.False(t, snapshot.Names(""))
	require.False(t, snapshot.Names("   "))
}
