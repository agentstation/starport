package catalog

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/availability"
)

// TestRoutabilityVerdictsAgreeWithTheRouteSet pins the invariant that makes the
// verdicts worth publishing: they are derived in the same pass as the routes,
// so they cannot drift from them. Every offering in the generation carries
// exactly one verdict, every routable verdict has a route, and every route has
// a routable verdict. Without this an operator reads a reachability claim that
// the planner does not honor.
func TestRoutabilityVerdictsAgreeWithTheRouteSet(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)

	providerID, offering := firstOffering(t, client.Catalog())
	require.NoError(t, plane.ReplaceAdapters([]AdapterAvailability{
		testAdapterAvailability(providerID, offering, true),
	}))
	snapshot := plane.Current()

	verdicts := snapshot.OfferingRoutability()
	require.NotEmpty(t, verdicts)

	routable := make(map[catalogs.OfferingKey]struct{})
	seen := make(map[catalogs.OfferingKey]struct{})
	for _, verdict := range verdicts {
		key := catalogs.OfferingKey{
			ProviderID: verdict.ProviderID, ProviderModelID: verdict.ProviderModelID,
		}
		_, duplicate := seen[key]
		require.Falsef(t, duplicate, "duplicate verdict for %s/%s",
			verdict.ProviderID, verdict.ProviderModelID)
		seen[key] = struct{}{}
		if verdict.Routable {
			require.Equal(t, RouteExclusionNone, verdict.Exclusion)
			routable[key] = struct{}{}
			continue
		}
		require.NotEqualf(t, RouteExclusionNone, verdict.Exclusion,
			"%s/%s is unroutable without a named exclusion",
			verdict.ProviderID, verdict.ProviderModelID)
	}

	// The verdict set covers the whole generation, not only what planning kept.
	total := 0
	for _, provider := range client.Catalog().Providers().List() {
		offerings, err := client.Catalog().ProviderOfferings(provider.ID)
		require.NoError(t, err)
		total += len(offerings)
	}
	require.Equal(t, total, len(verdicts))

	routes := snapshot.Routes()
	require.NotEmpty(t, routes)
	require.Equal(t, len(routes), len(routable))
	for _, route := range routes {
		_, marked := routable[route.Key()]
		require.Truef(t, marked, "route %s carries no routable verdict", route.ID())
	}
}

// TestOfferingWithNoSharedOperationReportsOperationUnsupported covers the drift
// this signal exists to catch: the catalog advertises an offering, the provider
// is registered and healthy, and no request can reach it because the adapter
// serves none of the operations the offering claims. That combination is
// invisible in circuit state, which keeps reporting the offering as healthy.
func TestOfferingWithNoSharedOperationReportsOperationUnsupported(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)

	providerID, offering := firstOffering(t, client.Catalog())
	adapter := testAdapterAvailability(providerID, offering, true)
	adapter.Operations = nil
	require.NoError(t, plane.ReplaceAdapters([]AdapterAvailability{adapter}))

	snapshot := plane.Current()
	require.Empty(t, snapshot.RoutesForProvider(providerID))
	require.Equal(t,
		RouteExclusionOperationUnsupported,
		exclusionFor(t, snapshot, providerID, offering.ProviderModelID),
	)
}

// TestProviderWithoutAdapterReportsAdapterNotReady separates the two ways an
// offering becomes unreachable. A provider with no compiled adapter fails for
// the whole provider, and the reason must say so rather than blaming the
// offering's own operations.
func TestProviderWithoutAdapterReportsAdapterNotReady(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)

	providerID, offering := firstOffering(t, client.Catalog())
	require.NoError(t, plane.ReplaceAdapters(nil))

	snapshot := plane.Current()
	require.Empty(t, snapshot.Routes())
	require.Equal(t,
		RouteExclusionAdapterNotReady,
		exclusionFor(t, snapshot, providerID, offering.ProviderModelID),
	)
}

// TestWithheldOfferingReportsOfferingUnavailable checks that a runtime
// availability decision moves the verdict with it, so the console does not keep
// calling a withheld offering reachable.
func TestWithheldOfferingReportsOfferingUnavailable(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)

	providerID, offering := firstOffering(t, client.Catalog())
	require.NoError(t, plane.ReplaceAdapters([]AdapterAvailability{
		testAdapterAvailability(providerID, offering, true),
	}))
	require.Equal(t,
		RouteExclusionNone,
		exclusionFor(t, plane.Current(), providerID, offering.ProviderModelID),
	)

	require.NoError(t, plane.PublishAvailability(availability.Snapshot{
		Revision: 1,
		Records: []availability.Record{{
			Offering: availability.Offering{
				ProviderID:      string(providerID),
				ProviderModelID: string(offering.ProviderModelID),
			},
			State: availability.StateUnavailable,
		}},
	}))
	require.Equal(t,
		RouteExclusionOfferingUnavailable,
		exclusionFor(t, plane.Current(), providerID, offering.ProviderModelID),
	)
}

func exclusionFor(
	t *testing.T,
	snapshot *RoutableSnapshot,
	providerID catalogs.ProviderID,
	providerModelID catalogs.ProviderModelID,
) RouteExclusion {
	t.Helper()
	for _, verdict := range snapshot.OfferingRoutability() {
		if verdict.ProviderID == providerID && verdict.ProviderModelID == providerModelID {
			return verdict.Exclusion
		}
	}
	t.Fatalf("no routability verdict for %s/%s", providerID, providerModelID)
	return RouteExclusionNone
}
