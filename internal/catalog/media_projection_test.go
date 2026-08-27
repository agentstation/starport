package catalog

import (
	"sort"
	"strings"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// mediaOperations names the five media operations the catalog carries. The
// projection is written to be operation-agnostic, which is what lets a media
// offering reach a route without a code change. That same property means no
// compiler error would appear if the projection started dropping media, so this
// list is the only place the expectation is written down.
var mediaOperations = []catalogs.ProviderOperation{
	catalogs.ProviderOperationImagesGenerations,
	catalogs.ProviderOperationImagesEdits,
	catalogs.ProviderOperationAudioSpeech,
	catalogs.ProviderOperationAudioTranscriptions,
	catalogs.ProviderOperationAudioTranslations,
}

// TestTheProjectionCarriesEveryMediaOperationTheCatalogNames is the end-to-end
// claim of Phase C at this seam. For each media operation it counts the
// offerings the generation publishes with a usable endpoint, projects the whole
// generation, and requires the route set to carry exactly that many. An
// off-by-any result means the projection silently dropped a media offering the
// operator can see in the catalog.
func TestTheProjectionCarriesEveryMediaOperationTheCatalogNames(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	source := client.Catalog()
	plane, err := Open(client)
	require.NoError(t, err)

	adapters, expected := mediaCensus(t, source)
	require.NoError(t, plane.ReplaceAdapters(adapters))

	projected := make(map[catalogs.ProviderOperation]int)
	for _, route := range plane.Current().Routes() {
		for _, operation := range route.Operations {
			projected[operation]++
			// A projected operation without its endpoint would route a
			// request to an empty URL.
			endpoint, found := route.Endpoint(operation)
			require.Truef(t, found, "%s carries %s with no endpoint", route.ID(), operation)
			require.NotEmpty(t, strings.TrimSpace(endpoint.URL))
		}
	}

	t.Logf("media route census: %v", projected)

	named := 0
	for _, operation := range mediaOperations {
		require.Equalf(t, expected[operation], projected[operation],
			"%s: the catalog names %d routable offerings and the projection carried %d",
			operation, expected[operation], projected[operation])
		if projected[operation] > 0 {
			named++
		}
	}
	// The generation must publish media somewhere, or the equality above would
	// hold at zero and prove nothing.
	require.Positivef(t, named, "the generation published no media offering at all")

	// Chat stays reachable in the same projection. Widening the operation
	// vocabulary must not cost the operations the gateway already served.
	require.Positive(t, projected[catalogs.ProviderOperationChatCompletions])
}

// TestTheMediaOperationSpellingIsPinned guards a silent failure. The seam that
// hands a catalog operation to the planner casts the string with no lookup, and
// the planner treats a name it does not serve as inert. A rename in the catalog
// would therefore remove media from routing with no error anywhere. These are
// the exact strings the planner, the transport descriptors, and the console all
// read, so the spelling is a contract rather than an implementation detail.
func TestTheMediaOperationSpellingIsPinned(t *testing.T) {
	require.Equal(t, []string{
		"images-generations",
		"images-edits",
		"audio-speech",
		"audio-transcriptions",
		"audio-translations",
	}, operationNames(mediaOperations))

	// The two operations the gateway already served are pinned by the same
	// rule, because the same cast carries them.
	require.Equal(t, []string{"chat-completions", "embeddings"}, operationNames(
		[]catalogs.ProviderOperation{
			catalogs.ProviderOperationChatCompletions,
			catalogs.ProviderOperationEmbeddings,
		},
	))
}

func operationNames(operations []catalogs.ProviderOperation) []string {
	names := make([]string, 0, len(operations))
	for _, operation := range operations {
		names = append(names, string(operation))
	}
	return names
}

// mediaCensus builds one adapter per provider that serves everything its
// offerings claim, and counts the offerings that should reach a route for each
// media operation. Declaring every operation removes the adapter as a variable,
// so a shortfall in the projection can only come from the projection.
func mediaCensus(
	t *testing.T,
	source *catalogs.Catalog,
) ([]AdapterAvailability, map[catalogs.ProviderOperation]int) {
	t.Helper()
	require.NotNil(t, source)

	adapters := make([]AdapterAvailability, 0)
	expected := make(map[catalogs.ProviderOperation]int)
	for _, provider := range source.Providers().List() {
		offerings, err := source.ProviderOfferings(provider.ID)
		require.NoError(t, err)
		if len(offerings) == 0 {
			continue
		}
		operations := make(map[catalogs.ProviderOperation]struct{})
		endpointTypes := make(map[catalogs.EndpointType]struct{})
		for _, offering := range offerings {
			if offering.Lifecycle == catalogs.OfferingLifecycleRetired ||
				offering.Availability == catalogs.OfferingAvailabilityUnavailable {
				continue
			}
			for _, operation := range offering.Service.Operations {
				operations[operation] = struct{}{}
				endpoint, found := offering.Endpoint(operation)
				if !found || strings.TrimSpace(endpoint.URL) == "" {
					continue
				}
				endpointTypes[endpoint.Type] = struct{}{}
				expected[operation]++
			}
		}
		adapters = append(adapters, AdapterAvailability{
			ProviderID:    provider.ID,
			Registered:    true,
			Operations:    sortedKeys(operations),
			EndpointTypes: sortedKeys(endpointTypes),
		})
	}
	return adapters, expected
}

func sortedKeys[Key ~string](values map[Key]struct{}) []Key {
	keys := make([]Key, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool { return keys[left] < keys[right] })
	return keys
}
