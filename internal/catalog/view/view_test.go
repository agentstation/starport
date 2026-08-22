package view

import (
	"math"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// fixtureSnapshot registers every offering of the named embedded-catalog
// providers so their routes are routable.
func fixtureSnapshot(t *testing.T, providerIDs ...catalogs.ProviderID) *runtimecatalog.RoutableSnapshot {
	t.Helper()
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	for _, providerID := range providerIDs {
		offerings, err := client.Catalog().ProviderOfferings(providerID)
		require.NoError(t, err)
		require.NotEmpty(t, offerings)
		operations := make(map[catalogs.ProviderOperation]struct{})
		endpointTypes := make(map[catalogs.EndpointType]struct{})
		for _, offering := range offerings {
			for _, operation := range offering.Service.Operations {
				operations[operation] = struct{}{}
			}
			for _, endpoint := range offering.Endpoints {
				endpointTypes[endpoint.Type] = struct{}{}
			}
		}
		availability := runtimecatalog.AdapterAvailability{
			ProviderID: providerID,
			Registered: true,
		}
		for operation := range operations {
			availability.Operations = append(availability.Operations, operation)
		}
		for endpointType := range endpointTypes {
			availability.EndpointTypes = append(availability.EndpointTypes, endpointType)
		}
		require.NoError(t, plane.SetAdapter(availability))
	}
	snapshot := plane.Current()
	require.NotNil(t, snapshot)
	return snapshot
}

func TestModelsNilSnapshot(t *testing.T) {
	require.Nil(t, Models(nil))
}

func TestModelsProjectionContract(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")
	models := Models(snapshot)
	require.NotEmpty(t, models)
	for _, model := range models {
		require.NotEmpty(t, model.ID)
		require.Equal(t, "model", model.Object)
		require.Equal(t, model.ID, model.CanonicalSlug)
		require.NotNil(t, model.Architecture)
	}
}

func TestModelsCreatedIsSet(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")
	for _, model := range Models(snapshot) {
		require.NotZero(t, model.Created)
	}
}

func TestFormatTokenPrice(t *testing.T) {
	require.Empty(t, formatTokenPrice(nil))
	require.Equal(t, "3e-06",
		formatTokenPrice(&catalogs.ModelTokenCost{PerToken: 0.000003}))
	// Per1M is the fallback when the per-token price is absent.
	require.Equal(t, "3e-06",
		formatTokenPrice(&catalogs.ModelTokenCost{Per1M: 3}))
	require.Equal(t, "0", formatTokenPrice(&catalogs.ModelTokenCost{}))
}

func TestBoundedModelIntClampsOverflow(t *testing.T) {
	require.Equal(t, int(math.MaxInt), boundedModelInt(math.MaxInt64))
	require.Equal(t, 42, boundedModelInt(42))
}

func TestSupportedModelParameters(t *testing.T) {
	definition := catalogs.ModelDefinition{}
	require.Nil(t, supportedModelParameters(definition))
	require.Nil(t, modelInputModalities(definition))
	require.Nil(t, modelOutputModalities(definition))

	definition.Capabilities.Features = &catalogs.ModelFeatures{
		Tools:           true,
		Temperature:     true,
		MaxOutputTokens: true,
	}
	require.Equal(t,
		[]string{"tools", "temperature", "max_tokens"},
		supportedModelParameters(definition),
	)
}

func TestProvidersNilSnapshot(t *testing.T) {
	require.Nil(t, Providers(nil, nil))
}

func TestProvidersProjectionContract(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")

	authQueries := make([]string, 0, 1)
	providers := Providers(snapshot, func(providerID string) bool {
		authQueries = append(authQueries, providerID)
		return true
	})
	require.Len(t, providers, 1)
	provider := providers[0]
	require.Equal(t, "anthropic", provider.ID)
	require.NotEmpty(t, provider.Name)
	require.NotEmpty(t, provider.Models)
	require.True(t, provider.RequiresAuth)
	require.Equal(t, []string{"anthropic"}, authQueries)
	require.IsIncreasing(t, provider.Capabilities)

	// A nil requiresAuth projects RequiresAuth false instead of panicking.
	unauth := Providers(snapshot, nil)
	require.Len(t, unauth, 1)
	require.False(t, unauth[0].RequiresAuth)
}

func TestModelsCarryEveryOffering(t *testing.T) {
	snapshot := fixtureSnapshot(t, "google-ai-studio", "google-vertex")
	var multi *ModelInfo
	models := Models(snapshot)
	for index := range models {
		if len(models[index].Offerings) >= 2 {
			multi = &models[index]
			break
		}
	}
	require.NotNil(t, multi,
		"a model served by two providers must list every offering")
	providers := make(map[string]struct{})
	for _, offering := range multi.Offerings {
		require.NotEmpty(t, offering.Provider)
		require.NotEmpty(t, offering.ProviderModelID)
		providers[offering.Provider] = struct{}{}
	}
	require.GreaterOrEqual(t, len(providers), 2,
		"offerings must span both serving providers")
}

func TestModelsCarryDefinitionFacts(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")
	models := Models(snapshot)
	require.NotEmpty(t, models)
	var withAuthors, withOfferings int
	for _, model := range models {
		if len(model.Authors) > 0 {
			withAuthors++
			require.NotEmpty(t, model.Authors[0].ID)
		}
		if len(model.Offerings) > 0 {
			withOfferings++
		}
	}
	require.NotZero(t, withAuthors,
		"anthropic definitions carry author ids in the catalog")
	require.Equal(t, len(models), withOfferings,
		"every routable model has at least one offering")
}

func TestProvidersCarryPolicyFacts(t *testing.T) {
	snapshot := fixtureSnapshot(t, "anthropic")
	providers := Providers(snapshot, nil)
	require.Len(t, providers, 1)
	provider := providers[0]
	require.NotEmpty(t, provider.Headquarters,
		"anthropic carries headquarters in the catalog")
	require.NotNil(t, provider.Policies,
		"anthropic carries policy facts in the catalog")
	require.NotEmpty(t, provider.Policies.PrivacyPolicyURL)
}

func TestEndpointsProjection(t *testing.T) {
	require.Empty(t, Endpoints(nil, "any/model"))

	snapshot := fixtureSnapshot(t, "anthropic")
	models := Models(snapshot)
	require.NotEmpty(t, models)
	endpoints := Endpoints(snapshot, models[0].ID)
	require.NotEmpty(t, endpoints)
	for _, endpoint := range endpoints {
		require.Equal(t, "anthropic", endpoint.Provider)
		require.NotEmpty(t, endpoint.Endpoint)
		require.True(t, endpoint.Available)
	}

	missing := Endpoints(snapshot, "missing/model")
	require.NotNil(t, missing)
	require.Empty(t, missing)
}
