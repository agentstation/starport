package catalog

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/routing"
)

// recognitionOffering builds one offering that serves document recognition
// beside chat, which is the shape the shipped catalog carries. The page price
// is the only variable, because it is the only fact this rule reads.
func recognitionOffering(page *float64) catalogs.ProviderOffering {
	pricing := &catalogs.ModelPricing{Currency: "USD"}
	if page != nil {
		pricing.Operations = &catalogs.ModelOperationPricing{PageInput: page}
	}
	return catalogs.ProviderOffering{
		ProviderID:      "google-ai-studio",
		ProviderModelID: "gemini-2.5-flash",
		DefinitionID:    "google/gemini-2.5-flash",
		Pricing:         pricing,
		Service: catalogs.ProviderOfferingServiceCapabilities{
			Operations: []catalogs.ProviderOperation{
				catalogs.ProviderOperationChatCompletions,
				catalogs.ProviderOperationDocumentsRecognition,
			},
		},
		Endpoints: []catalogs.ProviderOfferingEndpoint{
			{
				Operation: catalogs.ProviderOperationChatCompletions,
				Type:      "google",
				URL:       "https://example.invalid/v1beta/models/gemini-2.5-flash:generateContent",
			},
			{
				Operation: catalogs.ProviderOperationDocumentsRecognition,
				Type:      "google",
				URL:       "https://example.invalid/v1beta/models/gemini-2.5-flash:generateContent",
			},
		},
	}
}

func recognitionAdapter() AdapterAvailability {
	return AdapterAvailability{
		ProviderID: "google-ai-studio",
		Registered: true,
		Operations: []catalogs.ProviderOperation{
			catalogs.ProviderOperationChatCompletions,
			catalogs.ProviderOperationDocumentsRecognition,
		},
		EndpointTypes: []catalogs.EndpointType{"google"},
	}
}

func priceOf(value float64) *float64 { return &value }

// TestAPricedRecognitionOfferingKeepsBothOperations is the case the catalog
// data produces today. Recognition is the first operation a provider serves on
// the same path it serves chat, so the two have to survive planning together.
func TestAPricedRecognitionOfferingKeepsBothOperations(t *testing.T) {
	operations, endpoints, unpriced := compatibleOfferingService(
		recognitionAdapter(),
		recognitionOffering(priceOf(0.0000774)),
	)

	require.False(t, unpriced)
	require.Equal(t, []catalogs.ProviderOperation{
		catalogs.ProviderOperationChatCompletions,
		catalogs.ProviderOperationDocumentsRecognition,
	}, operations)
	require.Len(t, endpoints, 2)
}

// TestARecognitionOfferingWithNoPagePriceLosesTheOperation states the rule
// PLG3 adds. A page price is not decoration on an offering that already has
// token prices: it is the only number that turns a recognized page into a
// charge. Without it the gateway would answer a recognition request, pay the
// provider for it, and record no cost against the account that asked.
//
// The chat operation is deliberately untouched. A missing page price is a
// statement about one operation, and taking the whole model away would refuse a
// caller who never asked to read a document.
func TestARecognitionOfferingWithNoPagePriceLosesTheOperation(t *testing.T) {
	for _, test := range []struct {
		name string
		page *float64
	}{
		{name: "no operation prices at all", page: nil},
		{name: "a negative page price", page: priceOf(-0.01)},
	} {
		t.Run(test.name, func(t *testing.T) {
			offering := recognitionOffering(test.page)
			require.ErrorIs(t,
				billableOperation(offering, catalogs.ProviderOperationDocumentsRecognition),
				ErrMissingPagePrice,
			)

			operations, endpoints, unpriced := compatibleOfferingService(recognitionAdapter(), offering)
			require.True(t, unpriced)
			require.Equal(t,
				[]catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
				operations,
			)
			require.Len(t, endpoints, 1)
		})
	}
}

// TestAFreeRecognitionOfferingIsBillable separates two different facts that
// both read as "no charge". A stated price of zero is a provider decision. A
// missing price is a catalog gap, and the gateway cannot tell the caller which
// one it is unless the catalog does.
func TestAFreeRecognitionOfferingIsBillable(t *testing.T) {
	require.NoError(t, billableOperation(
		recognitionOffering(priceOf(0)),
		catalogs.ProviderOperationDocumentsRecognition,
	))
}

// TestEveryOtherOperationIsBillableWithoutAPagePrice keeps the rule narrow. A
// page price is required of recognition alone, so a chat or embedding offering
// that carries none still plans.
func TestEveryOtherOperationIsBillableWithoutAPagePrice(t *testing.T) {
	offering := recognitionOffering(nil)
	for _, operation := range []catalogs.ProviderOperation{
		catalogs.ProviderOperationChatCompletions,
		catalogs.ProviderOperationEmbeddings,
		catalogs.ProviderOperationImagesGenerations,
		catalogs.ProviderOperationAudioTranscriptions,
		catalogs.ProviderOperationVideosGenerations,
	} {
		require.NoErrorf(t, billableOperation(offering, operation), "%s", operation)
	}
}

// TestAnUnpricedRecognitionOfferingReportsItsOwnExclusion covers the offering
// that serves recognition and nothing else. Planning drops it, and the reason
// an operator reads has to name a catalog price rather than a missing adapter,
// because those two have different fixes.
func TestAnUnpricedRecognitionOfferingReportsItsOwnExclusion(t *testing.T) {
	offering := recognitionOffering(nil)
	offering.Service.Operations = []catalogs.ProviderOperation{
		catalogs.ProviderOperationDocumentsRecognition,
	}

	operations, _, unpriced := compatibleOfferingService(recognitionAdapter(), offering)
	require.Empty(t, operations)
	require.True(t, unpriced)
	require.NotEqual(t, RouteExclusionOperationUnsupported, RouteExclusionOperationUnpriced)
}

// TestTheShippedCatalogPricesEveryRecognitionRoute reads the real generation.
// Starmap derives the page price from Google's published page-to-token rule,
// and this is the assertion that the derived number reached Starport rather
// than being dropped somewhere between the two repositories.
func TestTheShippedCatalogPricesEveryRecognitionRoute(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	source := client.Catalog()

	priced := 0
	for _, provider := range source.Providers().List() {
		offerings, err := source.ProviderOfferings(provider.ID)
		require.NoError(t, err)
		for _, offering := range offerings {
			if !offering.Supports(catalogs.ProviderOperationDocumentsRecognition) {
				continue
			}
			priced++
			require.NoErrorf(t,
				billableOperation(offering, catalogs.ProviderOperationDocumentsRecognition),
				"%s/%s", provider.ID, offering.ProviderModelID,
			)
			require.NotNil(t, offering.Limits)
			require.Positivef(t, offering.Limits.DocumentPages,
				"%s/%s states no page limit", provider.ID, offering.ProviderModelID)
		}
	}

	// The census PLG3 records. A drop to zero means the catalog stopped naming
	// the operation, which is a silent regression rather than a failing route.
	require.Equal(t, 11, priced)
}

// TestTheRecognitionOperationKeepsItsWireValue holds the one string that
// crosses three boundaries with no compiler between any two of them.
//
// The catalog data writes it into a provider's operation list, the router names
// the same operation for its own planning, and Starport reads both out of one
// snapshot. A rename on either side still compiles and still passes every test
// that goes through the constants. It resolves no route.
func TestTheRecognitionOperationKeepsItsWireValue(t *testing.T) {
	require.Equal(t, "documents-recognition", string(catalogs.ProviderOperationDocumentsRecognition))
	require.Equal(t,
		string(catalogs.ProviderOperationDocumentsRecognition),
		string(routing.OperationDocumentsRecognition),
	)
}
